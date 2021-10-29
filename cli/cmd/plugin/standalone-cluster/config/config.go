// Copyright 2020-2021 VMware Tanzu Community Edition contributors. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	ClusterConfigFile = "ClusterConfigFile"
	ClusterName       = "ClusterName"
	Tty               = "Tty"
	TKRLocation       = "TkrLocation"
	Provider          = "Provider"
	Cni               = "Cni"
	PodCIDR           = "PodCidr"
	ServiceCIDR       = "ServiceCidr"
	configDir         = ".config"
	tanzuConfigDir    = "tanzu"
	yamlIndent        = 2
)

var defaultConfigValues = map[string]string{
	TKRLocation: "projects.registry.vmware.com/tce/tkr:v1.21.5",
	Provider:    "kind",
	Cni:         "antrea",
	PodCIDR:     "10.244.0.0/16",
	ServiceCIDR: "10.96.0.0/16",
	Tty:         "true",
}

// PortMap is the mapping between a host port and a container port.
type PortMap struct {
	// HostPort is the port on the host machine.
	HostPort int `yaml:"HostPort"`
	// ContainerPort is the port on the container to map to.
	ContainerPort int `yaml:"ContainerPort"`
}

// LocalClusterConfig contains all the configuration settings for creating a
// local Tanzu cluster.
type LocalClusterConfig struct {
	// ClusterName is the name of the cluster.
	ClusterName string `yaml:"ClusterName"`
	// KubeconfigPath is the serialized path to the kubeconfig to use.
	KubeconfigPath string `yaml:"KubeconfigPath"`
	// NodeImage is the host OS image to use for Kubernetes nodes.
	// It is typically resolved, automatically, in the Taznu Kubernetes Release (TKR) BOM,
	// but also can be overridden in configuration.
	NodeImage string `yaml:"NodeImage"`
	// Provider is the local infrastructure provider to use (e.g. kind).
	Provider string `yaml:"Provider"`
	// ProviderConfiguration offers optional provider-specific configuration.
	// The exact keys and values accepted are determined by the provider.
	ProviderConfiguration map[string]interface{} `yaml:"ProviderConfiguration"`
	// CNI is the networking CNI to use in the cluster. Default is antrea.
	Cni string `yaml:"Cni"`
	// CNIConfiguration offers optional cni-plugin specific configuration.
	// The exact keys and values accepted are determined by the CNI choice.
	CNIConfiguration map[string]interface{} `yaml:"CniConfiguration"`
	// PodCidr is the Pod CIDR range to assign pod IP addresses.
	PodCidr string `yaml:"PodCidr"`
	// ServiceCidr is the Service CIDR range to assign service IP addresses.
	ServiceCidr string `yaml:"ServiceCidr"`
	// TkrLocation is the path to the Tanzu Kubernetes Release (TKR) data.
	TkrLocation string `yaml:"TkrLocation"`
	// PortsToForward contains a mapping of host to container ports that should
	// be exposed.
	PortsToForward []PortMap `yaml:"PortsToForward"`
	// TTY specifies whether the output of commands can be stylized and/or interactive.
	Tty string `yaml:"Tty"`
}

// KubeConfigPath gets the full path to the KubeConfig for this local cluster.
func (lcc *LocalClusterConfig) KubeConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), configDir, tanzuConfigDir, lcc.ClusterName+".yaml")
}

// InitializeConfiguration determines the configuration to use for cluster creation.
//
// There are three places where configuration comes from:
// - default settings
// - configuration file
// - environment variables
// - command line arguments
//
// The effective configuration is determined by combining these sources, in ascending
// order of preference listed. So env variables override values in the config file,
// and explicit CLI arguments override config file and env variable values.
func InitializeConfiguration(commandArgs map[string]string) (*LocalClusterConfig, error) {
	config := &LocalClusterConfig{}

	// First, populate values based on a supplied config file
	if commandArgs[ClusterConfigFile] != "" {
		configData, err := os.ReadFile(commandArgs[ClusterConfigFile])
		if err != nil {
			return nil, err
		}

		err = yaml.Unmarshal(configData, config)
		if err != nil {
			return nil, err
		}
	}

	// Loop through and look up each field
	element := reflect.ValueOf(config).Elem()
	for i := 0; i < element.NumField(); i++ {
		field := element.Type().Field(i)
		if field.Type.Kind() != reflect.String {
			// Not supporting more complex data types yet, will need to see if and
			// how to do this.
			continue
		}

		// Use the yaml name if provided so it matches what we serialize to file
		fieldName := field.Tag.Get("yaml")
		if fieldName == "" {
			fieldName = field.Name
		}

		// Check if an explicit value was passed in
		if value, ok := commandArgs[fieldName]; ok && value != "" {
			element.FieldByName(field.Name).SetString(value)
		} else if value := os.Getenv(fieldNameToEnvName(fieldName)); value != "" {
			// See if there is an environment variable set for this field
			element.FieldByName(field.Name).SetString(value)
		}

		// Only set to the default value if it hasn't been set already
		if element.FieldByName(field.Name).String() == "" {
			if value, ok := defaultConfigValues[fieldName]; ok {
				element.FieldByName(field.Name).SetString(value)
			}
		}
	}

	// Make sure cluster name was either set on the command line or in the config
	// file.
	if config.ClusterName == "" {
		return nil, fmt.Errorf("cluster name must be provided")
	}

	// Sanatize the filepath for the provided kubeconfig
	config.KubeconfigPath = sanatizeKubeconfigPath(config.KubeconfigPath)

	return config, nil
}

// fieldNameToEnvName converts the config values yaml name to its expected env
// variable name.
func fieldNameToEnvName(field string) string {
	namedArray := []string{"TANZU"}
	re := regexp.MustCompile(`[A-Z][^A-Z]*`)
	allWords := re.FindAllString(field, -1)
	for _, word := range allWords {
		namedArray = append(namedArray, strings.ToUpper(word))
	}
	return strings.Join(namedArray, "_")
}

func sanatizeKubeconfigPath(path string) string {
	var builder string

	// handle tildas at the beginning of the path
	if strings.HasPrefix(path, "~/") {
		usr, _ := user.Current()
		builder = filepath.Join(builder, usr.HomeDir)
		path = path[2:]
	}

	builder = filepath.Join(builder, path)

	return builder
}

// RenderConfigToFile take a file path and serializes the configuration data to that path. It expects the path
// to not exist, if it does, an error is returned.
func RenderConfigToFile(filePath string, config interface{}) error {
	// check if file exists
	// determine if directory pre-exists
	_, err := os.ReadDir(filePath)

	// if it does not exist, which is expected, create it
	if !os.IsNotExist(err) {
		return fmt.Errorf("failed to create config file at %q, does it already exist", filePath)
	}

	var rawConfig bytes.Buffer
	yamlEncoder := yaml.NewEncoder(&rawConfig)
	yamlEncoder.SetIndent(yamlIndent)

	err = yamlEncoder.Encode(config)
	if err != nil {
		return fmt.Errorf("failed to render configuration file. Error: %s", err.Error())
	}
	err = os.WriteFile(filePath, rawConfig.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("failed to write rawConfig file. Error: %s", err.Error())
	}
	// if it does, return an error
	// otherwise, write config to file
	return nil
}

// RenderFileToConfig reads in configuration from a file and returns the
// LocalClusterConfig structure based on it. If the file does not exist or there
// is a problem reading the configuration from it an error is returned.
func RenderFileToConfig(filePath string) (*LocalClusterConfig, error) {
	d, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading config file. Error: %s", err.Error())
	}
	lcc := &LocalClusterConfig{}
	err = yaml.Unmarshal(d, lcc)
	if err != nil {
		return nil, fmt.Errorf("configuration at %s was invalid. Error: %s", filePath, err.Error())
	}

	return lcc, nil
}
