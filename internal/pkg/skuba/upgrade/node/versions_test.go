/*
 * Copyright (c) 2019 SUSE LLC.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package node

import (
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/version"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"

	"github.com/SUSE/skuba/internal/pkg/skuba/kubernetes"
)

type TestVersionInquirer struct {
	AvailableVersions kubernetes.KubernetesVersions
}

func (ti TestVersionInquirer) AvailablePlatformVersions() []*version.Version {
	return kubernetes.AvailableVersionsForMap(ti.AvailableVersions)
}

func (ti TestVersionInquirer) NodeVersionInfoForClusterVersion(node *corev1.Node, clusterVersion *version.Version) kubernetes.NodeVersionInfo {
	res := kubernetes.NodeVersionInfo{
		Nodename:                node.ObjectMeta.Name,
		ContainerRuntimeVersion: version.MustParseSemantic(kubernetes.ComponentVersionWithAvailableVersions(kubernetes.ContainerRuntime, clusterVersion, ti.AvailableVersions)),
		KubeletVersion:          version.MustParseSemantic(kubernetes.ComponentVersionWithAvailableVersions(kubernetes.Kubelet, clusterVersion, ti.AvailableVersions)),
	}
	if kubernetes.IsControlPlane(node) {
		res.APIServerVersion = version.MustParseSemantic(kubernetes.ComponentVersionWithAvailableVersions(kubernetes.Hyperkube, clusterVersion, ti.AvailableVersions))
		res.ControllerManagerVersion = version.MustParseSemantic(kubernetes.ComponentVersionWithAvailableVersions(kubernetes.Hyperkube, clusterVersion, ti.AvailableVersions))
		res.SchedulerVersion = version.MustParseSemantic(kubernetes.ComponentVersionWithAvailableVersions(kubernetes.Hyperkube, clusterVersion, ti.AvailableVersions))
		res.EtcdVersion = version.MustParseSemantic(kubernetes.ComponentVersionWithAvailableVersions(kubernetes.Etcd, clusterVersion, ti.AvailableVersions))
	}
	return res
}

func isControlPlane(nodeVersionInfo kubernetes.NodeVersionInfo) bool {
	return nodeVersionInfo.IsControlPlane()
}

func TestNodesVersionAligned(t *testing.T) {
	var versions = []struct {
		name                   string
		currentClusterVersion  *version.Version
		allNodesVersioningInfo kubernetes.NodeVersionInfoMap
		nodeConsidered         func(kubernetes.NodeVersionInfo) bool
		expectedAligned        bool
	}{
		{
			name:                  "apiserver and kubelet aligned",
			currentClusterVersion: version.MustParseSemantic("v1.14.0"),
			allNodesVersioningInfo: kubernetes.NodeVersionInfoMap{
				"cp1": {
					Nodename:         "cp1",
					APIServerVersion: version.MustParseSemantic("v1.14.0"),
					KubeletVersion:   version.MustParseSemantic("v1.14.0"),
				},
			},
			nodeConsidered:  isControlPlane,
			expectedAligned: true,
		},
		{
			name:                  "apiserver not aligned (by patch) and kubelet aligned",
			currentClusterVersion: version.MustParseSemantic("v1.14.9"),
			allNodesVersioningInfo: kubernetes.NodeVersionInfoMap{
				"cp1": {
					Nodename:         "cp1",
					APIServerVersion: version.MustParseSemantic("v1.14.0"),
					KubeletVersion:   version.MustParseSemantic("v1.14.9"),
				},
			},
			nodeConsidered:  isControlPlane,
			expectedAligned: true,
		},
		{
			name:                  "apiserver not aligned (by minor) and kubelet aligned",
			currentClusterVersion: version.MustParseSemantic("v1.14.0"),
			allNodesVersioningInfo: kubernetes.NodeVersionInfoMap{
				"cp1": {
					Nodename:         "cp1",
					APIServerVersion: version.MustParseSemantic("v1.13.0"),
					KubeletVersion:   version.MustParseSemantic("v1.14.0"),
				},
			},
			nodeConsidered:  isControlPlane,
			expectedAligned: false,
		},
		{
			name:                  "apiserver aligned and kubelet not aligned (by patch)",
			currentClusterVersion: version.MustParseSemantic("v1.14.9"),
			allNodesVersioningInfo: kubernetes.NodeVersionInfoMap{
				"cp1": {
					Nodename:         "cp1",
					APIServerVersion: version.MustParseSemantic("v1.14.9"),
					KubeletVersion:   version.MustParseSemantic("v1.14.0"),
				},
			},
			nodeConsidered:  isControlPlane,
			expectedAligned: true,
		},
		{
			name:                  "apiserver aligned and kubelet not aligned (by minor)",
			currentClusterVersion: version.MustParseSemantic("v1.14.0"),
			allNodesVersioningInfo: kubernetes.NodeVersionInfoMap{
				"cp1": {
					Nodename:         "cp1",
					APIServerVersion: version.MustParseSemantic("v1.14.0"),
					KubeletVersion:   version.MustParseSemantic("v1.13.0"),
				},
			},
			nodeConsidered:  isControlPlane,
			expectedAligned: false,
		},
		{
			name:                  "control plane aligned and worker node unaligned",
			currentClusterVersion: version.MustParseSemantic("v1.14.0"),
			allNodesVersioningInfo: kubernetes.NodeVersionInfoMap{
				"cp1": {
					Nodename:         "cp1",
					APIServerVersion: version.MustParseSemantic("v1.14.0"),
					KubeletVersion:   version.MustParseSemantic("v1.14.0"),
				},
				"worker1": {
					Nodename:       "worker1",
					KubeletVersion: version.MustParseSemantic("v1.12.0"),
				},
			},
			nodeConsidered:  isControlPlane,
			expectedAligned: true,
		},
	}
	for _, tt := range versions {
		tt := tt // Parallel testing
		t.Run(tt.name, func(t *testing.T) {
			aligned := nodesVersionAligned(tt.currentClusterVersion, tt.allNodesVersioningInfo, tt.nodeConsidered)
			if aligned != tt.expectedAligned {
				t.Errorf("align result (%v) does not match expectation (%v)", aligned, tt.expectedAligned)
			}
		})
	}
}

type nodeRole uint

const (
	controlPlane nodeRole = iota
	worker
)

func nodeVersion(node, nodeVersion string, nodeRole nodeRole) kubernetes.NodeVersionInfo {
	res := kubernetes.NodeVersionInfo{
		Nodename:                node,
		ContainerRuntimeVersion: version.MustParseSemantic(fmt.Sprintf("v%s", nodeVersion)),
		KubeletVersion:          version.MustParseSemantic(fmt.Sprintf("v%s", nodeVersion)),
	}
	if nodeRole == controlPlane {
		res.APIServerVersion = version.MustParseSemantic(fmt.Sprintf("v%s", nodeVersion))
		res.ControllerManagerVersion = version.MustParseSemantic(fmt.Sprintf("v%s", nodeVersion))
		res.SchedulerVersion = version.MustParseSemantic(fmt.Sprintf("v%s", nodeVersion))
		res.EtcdVersion = version.MustParseSemantic("3.3.11")
	}
	return res
}

func nodeVersionMap(controlPlaneNodes, workerNodes map[string]string) kubernetes.NodeVersionInfoMap {
	res := kubernetes.NodeVersionInfoMap{}
	for node, version := range controlPlaneNodes {
		res[node] = nodeVersion(node, version, controlPlane)
	}
	for node, version := range workerNodes {
		res[node] = nodeVersion(node, version, worker)
	}
	return res
}

func controlPlaneNode(name string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				kubeadmconstants.LabelNodeRoleMaster: "",
			},
		},
	}
}

func workerNode(name string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func versionInquirer(versions ...string) kubernetes.VersionInquirer {
	res := TestVersionInquirer{
		AvailableVersions: kubernetes.KubernetesVersions{},
	}
	for _, version := range versions {
		res.AvailableVersions[version] = kubernetes.KubernetesVersion{
			ControlPlaneComponentsVersion: kubernetes.ControlPlaneComponentsVersion{
				HyperkubeVersion: fmt.Sprintf("v%s", version),
				EtcdVersion:      "3.3.11",
			},
			ComponentsVersion: kubernetes.ComponentsVersion{
				ContainerRuntimeVersion: version,
				KubeletVersion:          version,
				ToolingVersion:          "0.1.0",
				CoreDNSVersion:          "1.2.6",
				PauseVersion:            "3.1",
			},
			AddonsVersion: kubernetes.AddonsVersion{
				kubernetes.Cilium: &kubernetes.AddonVersion{Version: "1.5.3", ManifestVersion: 0},
				kubernetes.Kured:  &kubernetes.AddonVersion{Version: "1.2.0", ManifestVersion: 0},
			},
		}
	}
	return res
}

func TestControlPlaneUpdateStatusWithAvailableVersions(t *testing.T) {
	var versions = []struct {
		name                                        string
		currentClusterVersion                       *version.Version
		versionInquirer                             kubernetes.VersionInquirer
		allNodesVersioningInfo                      kubernetes.NodeVersionInfoMap
		node                                        corev1.Node
		client                                      clientset.Interface
		expectedNodeVersionInfoUpdate               NodeVersionInfoUpdate
		expectedIsFirstControlPlaneNodeToBeUpgraded bool
		expectedHasMajorOrMinorUpdate               bool
		expectedIsUpdated                           bool
		expectedError                               bool
	}{
		{
			name:                   "first control plane to be upgraded; no upgrades available",
			currentClusterVersion:  version.MustParseSemantic("v1.14.0"),
			versionInquirer:        versionInquirer("1.14.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.14.0"}, map[string]string{}),
			node:                   controlPlaneNode("cp1"),
			client: fake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				Data: map[string]string{
					kubeadmconstants.ClusterConfigurationConfigMapKey: `
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration
kubernetesVersion: v1.14.0
`,
				},
			}),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("cp1", "1.14.0", controlPlane),
				Update:  nodeVersion("cp1", "1.14.0", controlPlane),
			},
			expectedIsFirstControlPlaneNodeToBeUpgraded: true,
			expectedHasMajorOrMinorUpdate:               false,
			expectedIsUpdated:                           true,
		},
		{
			name:                   "first control plane to be upgraded; upgrades available",
			currentClusterVersion:  version.MustParseSemantic("v1.14.0"),
			versionInquirer:        versionInquirer("1.14.0", "1.15.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.14.0"}, map[string]string{}),
			node:                   controlPlaneNode("cp1"),
			client: fake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				Data: map[string]string{
					kubeadmconstants.ClusterConfigurationConfigMapKey: `
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration
kubernetesVersion: v1.14.0
`,
				},
			}),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("cp1", "1.14.0", controlPlane),
				Update:  nodeVersion("cp1", "1.15.0", controlPlane),
			},
			expectedIsFirstControlPlaneNodeToBeUpgraded: true,
			expectedHasMajorOrMinorUpdate:               true,
			expectedIsUpdated:                           false,
		},
		{
			name:                   "secondary control plane to be upgraded; upgrades available",
			currentClusterVersion:  version.MustParseSemantic("v1.15.0"),
			versionInquirer:        versionInquirer("1.14.0", "1.15.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.15.0", "cp2": "1.14.0"}, map[string]string{}),
			node:                   controlPlaneNode("cp2"),
			client: fake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				Data: map[string]string{
					kubeadmconstants.ClusterConfigurationConfigMapKey: `
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration
kubernetesVersion: v1.15.0
`,
				},
			}),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("cp2", "1.14.0", controlPlane),
				Update:  nodeVersion("cp2", "1.15.0", controlPlane),
			},
			expectedIsFirstControlPlaneNodeToBeUpgraded: false,
			expectedHasMajorOrMinorUpdate:               true,
			expectedIsUpdated:                           false,
		},
		{
			name:                   "first control plane to be upgraded; outdated worker",
			currentClusterVersion:  version.MustParseSemantic("v1.14.0"),
			versionInquirer:        versionInquirer("1.13.0", "1.14.0", "1.15.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.14.0"}, map[string]string{"worker1": "1.13.0"}),
			node:                   controlPlaneNode("cp1"),
			expectedError:          true,
		},
		{
			name:                   "first control plane to be upgraded; patch version",
			currentClusterVersion:  version.MustParseSemantic("v1.15.0"),
			versionInquirer:        versionInquirer("1.15.0", "1.15.2"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.15.0"}, map[string]string{"worker1": "1.15.0"}),
			node:                   controlPlaneNode("cp1"),
			client: fake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				Data: map[string]string{
					kubeadmconstants.ClusterConfigurationConfigMapKey: `
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterConfiguration
kubernetesVersion: v1.15.0
`,
				},
			}),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("cp1", "1.15.0", controlPlane),
				Update:  nodeVersion("cp1", "1.15.2", controlPlane),
			},
			expectedIsFirstControlPlaneNodeToBeUpgraded: true,
			expectedHasMajorOrMinorUpdate:               false,
			expectedIsUpdated:                           false,
		},
		{
			name:                   "node name not found",
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.14.0"}, map[string]string{}),
			node:                   controlPlaneNode("cp0"),
			expectedError:          true,
		},
	}

	for _, tt := range versions {
		tt := tt // Parallel testing
		t.Run(tt.name, func(t *testing.T) {
			nodeVersionInfoUpdate, err := controlPlaneUpdateStatusWithAvailableVersions(tt.currentClusterVersion, tt.allNodesVersioningInfo, &tt.node, tt.versionInquirer)
			if tt.expectedError {
				if err == nil {
					t.Errorf("error expected on %s, but no error reported", tt.name)
				}
				return
			} else if err != nil {
				t.Errorf("error not expected on %s, but an error was reported (%v)", tt.name, err)
				return
			}

			if !reflect.DeepEqual(nodeVersionInfoUpdate, tt.expectedNodeVersionInfoUpdate) {
				t.Errorf("returned version info update (%v) does not match the expected one (%v)", nodeVersionInfoUpdate, tt.expectedNodeVersionInfoUpdate)
				return
			}

			isFirstControlPlaneNodeToBeUpgraded, err := nodeVersionInfoUpdate.IsFirstControlPlaneNodeToBeUpgraded(tt.client)
			if err != nil {
				t.Errorf("error not expected on %s, but an error was reported (%v)", tt.name, err)
				return
			}
			if isFirstControlPlaneNodeToBeUpgraded != tt.expectedIsFirstControlPlaneNodeToBeUpgraded {
				t.Errorf("got %t, expect %t", isFirstControlPlaneNodeToBeUpgraded, tt.expectedIsFirstControlPlaneNodeToBeUpgraded)
				return
			}

			hasMajorOrMinorUpdate := nodeVersionInfoUpdate.HasMajorOrMinorUpdate()
			if hasMajorOrMinorUpdate != tt.expectedHasMajorOrMinorUpdate {
				t.Errorf("got %t, expect %t", hasMajorOrMinorUpdate, tt.expectedHasMajorOrMinorUpdate)
				return
			}

			isUpdated := nodeVersionInfoUpdate.IsUpdated()
			if isUpdated != tt.expectedIsUpdated {
				t.Errorf("got %t, expect %t", isUpdated, tt.expectedIsUpdated)
			}
		})
	}
}

func TestWorkerUpdateStatusWithAvailableVersions(t *testing.T) {
	latestVersion := kubernetes.LatestVersion().String()
	versions := []struct {
		name                          string
		currentClusterVersion         *version.Version
		versionInquirer               kubernetes.VersionInquirer
		allNodesVersioningInfo        kubernetes.NodeVersionInfoMap
		node                          corev1.Node
		expectedNodeVersionInfoUpdate NodeVersionInfoUpdate
		expectedHasMajorOrMinorUpdate bool
		expectedIsUpdated             bool
		expectedError                 bool
	}{
		{
			name:                   "worker same version as control plane; upgrades available",
			currentClusterVersion:  version.MustParseSemantic("v1.15.0"),
			versionInquirer:        versionInquirer("1.14.1", "1.15.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.14.1"}, map[string]string{"worker1": "1.14.1"}),
			node:                   workerNode("worker1"),
			expectedError:          true,
		},
		{
			name:                   "one worker same version as control plane; other worker has upgrade available",
			currentClusterVersion:  version.MustParseSemantic("v1.15.0"),
			versionInquirer:        versionInquirer("1.14.1", "1.15.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.15.0"}, map[string]string{"worker1": "1.14.1", "worker2": "1.15.0"}),
			node:                   workerNode("worker1"),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("worker1", "1.14.1", worker),
				Update:  nodeVersion("worker1", "1.15.0", worker),
			},
			expectedHasMajorOrMinorUpdate: true,
			expectedIsUpdated:             false,
		},
		{
			name:                   "worker; no upgrades available",
			currentClusterVersion:  version.MustParseSemantic(latestVersion),
			versionInquirer:        versionInquirer(latestVersion),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{}, map[string]string{"worker1": latestVersion}),
			node:                   workerNode("worker1"),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("worker1", latestVersion, worker),
				Update:  nodeVersion("worker1", latestVersion, worker),
			},
			expectedHasMajorOrMinorUpdate: false,
			expectedIsUpdated:             true,
		},
		{
			name:                   "worker with outdated control plane; upgrades available",
			currentClusterVersion:  version.MustParseSemantic("v1.15.0"),
			versionInquirer:        versionInquirer("1.14.0", "1.15.0"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.15.0", "cp2": "1.14.0"}, map[string]string{"worker1": "1.14.0"}),
			node:                   workerNode("worker1"),
			expectedError:          true,
		},
		{
			name:                   "worker with updated control plane; patch version",
			currentClusterVersion:  version.MustParseSemantic("v1.15.2"),
			versionInquirer:        versionInquirer("1.15.0", "1.15.2"),
			allNodesVersioningInfo: nodeVersionMap(map[string]string{"cp1": "1.15.2"}, map[string]string{"worker1": "1.15.0"}),
			node:                   workerNode("worker1"),
			expectedNodeVersionInfoUpdate: NodeVersionInfoUpdate{
				Current: nodeVersion("worker1", "1.15.0", worker),
				Update:  nodeVersion("worker1", "1.15.2", worker),
			},
			expectedHasMajorOrMinorUpdate: false,
			expectedIsUpdated:             false,
		},
	}

	for _, tt := range versions {
		tt := tt // Parallel testing
		t.Run(tt.name, func(t *testing.T) {
			nodeVersionInfoUpdate, err := workerUpdateStatusWithAvailableVersions(tt.currentClusterVersion, tt.allNodesVersioningInfo, &tt.node, tt.versionInquirer)
			if tt.expectedError {
				if err == nil {
					t.Errorf("error expected on %s, but no error reported", tt.name)
				}
				return
			} else if err != nil {
				t.Errorf("error not expected on %s, but an error was reported (%v)", tt.name, err)
				return
			}

			if !reflect.DeepEqual(nodeVersionInfoUpdate, tt.expectedNodeVersionInfoUpdate) {
				t.Errorf("returned version info update (%v) does not match the expected one (%v)", nodeVersionInfoUpdate, tt.expectedNodeVersionInfoUpdate)
			}

			hasMajorOrMinorUpdate := nodeVersionInfoUpdate.HasMajorOrMinorUpdate()
			if hasMajorOrMinorUpdate != tt.expectedHasMajorOrMinorUpdate {
				t.Errorf("got %t, expect %t", hasMajorOrMinorUpdate, tt.expectedHasMajorOrMinorUpdate)
				return
			}

			isUpdated := nodeVersionInfoUpdate.IsUpdated()
			if isUpdated != tt.expectedIsUpdated {
				t.Errorf("got %t, expect %t", isUpdated, tt.expectedIsUpdated)
				return
			}
		})
	}
}
