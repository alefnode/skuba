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
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/version"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmconfigutil "k8s.io/kubernetes/cmd/kubeadm/app/util/config"

	"github.com/SUSE/skuba/internal/pkg/skuba/addons"
	"github.com/SUSE/skuba/internal/pkg/skuba/deployments"
	"github.com/SUSE/skuba/internal/pkg/skuba/kubeadm"
	"github.com/SUSE/skuba/internal/pkg/skuba/kubernetes"
	"github.com/SUSE/skuba/internal/pkg/skuba/node"
	"github.com/SUSE/skuba/pkg/skuba"
	"github.com/SUSE/skuba/pkg/skuba/cloud"
)

// Bootstrap initializes the first master node of the cluster
//
// FIXME: being this a part of the go API accept the toplevel directory instead
//        of using the PWD
func Bootstrap(bootstrapConfiguration deployments.BootstrapConfiguration, target *deployments.Target) error {
	coreBootstrapDone := false

	if clientSet, err := kubernetes.GetAdminClientSet(); err == nil {
		_, err := clientSet.Discovery().ServerVersion()
		if err == nil {
			fmt.Printf("[bootstrap] node %q has already the core components bootstrapped\n", target.Target)
			coreBootstrapDone = true
		}
	}

	initConfiguration, err := node.LoadInitConfigurationFromFile(skuba.KubeadmInitConfFile())
	if err != nil {
		return errors.Wrapf(err, "could not parse %s file", skuba.KubeadmInitConfFile())
	}

	if !coreBootstrapDone {
		if err := coreBootstrap(initConfiguration, bootstrapConfiguration, target); err != nil {
			return err
		}
	}

	if err := downloadSecrets(target); err != nil {
		return err
	}

	// Load admin.conf after download secrets from remote bootstrapped master node.
	clientSet, err := kubernetes.GetAdminClientSet()
	if err != nil {
		return err
	}

	fmt.Printf("[bootstrap] deploying core add-ons on node %q\n", target.Target)
	versionToDeploy, err := version.ParseSemantic(initConfiguration.KubernetesVersion)
	if err != nil {
		return errors.Wrapf(err, "could not parse semantic version: %s", initConfiguration.KubernetesVersion)
	}
	addonConfiguration := addons.AddonConfiguration{
		ClusterVersion: versionToDeploy,
		ControlPlane:   initConfiguration.ControlPlaneEndpoint,
		ClusterName:    initConfiguration.ClusterName,
	}
	if err := addons.DeployAddons(clientSet, addonConfiguration, addons.SkipRenderIfConfigFilePresent); err != nil {
		return err
	}

	fmt.Printf("[bootstrap] successfully bootstrapped core add-ons on node %q\n", target.Target)
	return nil
}

// Takes care of bootstrapping the core components of the nodes, containerized add-ons are
// not handled here.
func coreBootstrap(initConfiguration *kubeadmapi.InitConfiguration, bootstrapConfiguration deployments.BootstrapConfiguration, target *deployments.Target) error {
	versionToDeploy := version.MustParseSemantic(initConfiguration.KubernetesVersion)

	if _, err := target.InstallNodePattern(deployments.KubernetesBaseOSConfiguration{
		CurrentVersion: versionToDeploy.String(),
	}); err != nil {
		return err
	}

	fmt.Println("[bootstrap] updating init configuration with target information")
	if err := node.AddTargetInformationToInitConfigurationWithClusterVersion(target, initConfiguration, versionToDeploy); err != nil {
		return errors.Wrap(err, "unable to add target information to init configuration")
	}

	if cloud.HasCloudIntegration() {
		if !cloud.ConfigHasRestrictedPermissions(skuba.OpenstackCloudConfFile()) {
			return errors.New(fmt.Sprintf("Cloud config file %s should be accessible only by the owner (eg 600)", skuba.OpenstackCloudConfFile()))
		}
		setCloudConfiguration(initConfiguration)
		setCloudConfigurationPath(initConfiguration)
	}

	setApiserverAdmissionPlugins(initConfiguration)

	finalInitConfigurationContents, err := kubeadmconfigutil.MarshalInitConfigurationToBytes(initConfiguration, schema.GroupVersion{
		Group:   "kubeadm.k8s.io",
		Version: kubeadm.GetKubeadmApisVersion(versionToDeploy),
	})

	if err != nil {
		return errors.Wrap(err, "could not marshal configuration")
	}

	fmt.Println("[bootstrap] writing init configuration for node")
	if err := ioutil.WriteFile(skuba.KubeadmInitConfFile(), finalInitConfigurationContents, 0600); err != nil {
		return errors.Wrap(err, "error writing init configuration")
	}

	var criConfigure string
	if _, err := os.Stat(skuba.CriDockerDefaultsConfFile()); err == nil {
		criConfigure = "cri.configure"
	}

	fmt.Println("[bootstrap] applying init configuration to node")
	err = target.Apply(
		bootstrapConfiguration,
		"kubeadm.reset",
		"kubernetes.bootstrap.upload-secrets",
		"kernel.load-modules",
		"kernel.configure-parameters",
		"apparmor.start",
		criConfigure,
		"cri.start",
		"kubelet.configure",
		"kubelet.enable",
		"kubeadm.init",
		"skuba-update.start",
	)
	if err != nil {
		return err
	}

	fmt.Printf("[bootstrap] successfully bootstrapped core components on node %q with Kubernetes: %q\n", target.Target, versionToDeploy.String())
	return nil
}

func downloadSecrets(target *deployments.Target) error {
	if err := os.MkdirAll(filepath.Join("pki", "etcd"), 0700); err != nil {
		return errors.Wrapf(err, "could not create %s folder", filepath.Join("pki", "etcd"))
	}

	fmt.Printf("[bootstrap] downloading secrets from bootstrapped node %q\n", target.Target)
	for _, secretLocation := range deployments.Secrets {
		secretData, err := target.DownloadFileContents(filepath.Join("/etc/kubernetes", secretLocation))
		if err != nil {
			return err
		}
		if err := ioutil.WriteFile(secretLocation, []byte(secretData), 0600); err != nil {
			return err
		}
	}

	return nil
}

func setApiserverAdmissionPlugins(initConfiguration *kubeadmapi.InitConfiguration) {
	if initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs == nil {
		initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs = map[string]string{}
	}
	// List of recommended plugins: https://git.io/JemEu
	defaultAdmissionPlugins := "NamespaceLifecycle,LimitRanger,ServiceAccount,TaintNodesByCondition,Priority,DefaultTolerationSeconds,DefaultStorageClass,PersistentVolumeClaimResize,MutatingAdmissionWebhook,ValidatingAdmissionWebhook,ResourceQuota"
	// Update the variable when updating kubeadm if needed: https://git.io/Jem4z
	kubeadmAdmissionPlugins := "NodeRestriction"
	skubaAdmissionPlugins := "PodSecurityPolicy"
	initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs["enable-admission-plugins"] = fmt.Sprintf("%s,%s,%s", kubeadmAdmissionPlugins, skubaAdmissionPlugins, defaultAdmissionPlugins)
}

func setCloudConfigurationPath(initConfiguration *kubeadmapi.InitConfiguration) {
	cloudVolume := []kubeadmapi.HostPathMount{}
	cloudConfig := kubeadmapi.HostPathMount{
		Name:      "cloud-config",
		HostPath:  skuba.OpenstackConfigRuntimeFile(),
		MountPath: skuba.OpenstackConfigRuntimeFile(),
		ReadOnly:  true,
		PathType:  "FileOrCreate",
	}
	cloudVolume = append(cloudVolume, cloudConfig)
	initConfiguration.APIServer.ControlPlaneComponent.ExtraVolumes = cloudVolume
	initConfiguration.ControllerManager.ExtraVolumes = cloudVolume
}

func setCloudConfiguration(initConfiguration *kubeadmapi.InitConfiguration) {
	if initConfiguration.NodeRegistration.KubeletExtraArgs == nil {
		initConfiguration.NodeRegistration.KubeletExtraArgs = map[string]string{}
	}
	initConfiguration.NodeRegistration.KubeletExtraArgs["cloud-provider"] = "openstack"
	initConfiguration.NodeRegistration.KubeletExtraArgs["cloud-config"] = skuba.OpenstackConfigRuntimeFile()

	if initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs == nil {
		initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs = map[string]string{}
	}
	initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs["cloud-provider"] = "openstack"
	initConfiguration.APIServer.ControlPlaneComponent.ExtraArgs["cloud-config"] = skuba.OpenstackConfigRuntimeFile()

	if initConfiguration.ControllerManager.ExtraArgs == nil {
		initConfiguration.ControllerManager.ExtraArgs = map[string]string{}
	}
	initConfiguration.ControllerManager.ExtraArgs["cloud-provider"] = "openstack"
	initConfiguration.ControllerManager.ExtraArgs["cloud-config"] = skuba.OpenstackConfigRuntimeFile()
}
