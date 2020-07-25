// Package aws - aws provider.
// Common functions of aws provider.
package aws

import (
	"fmt"
	"time"

	"github.com/apex/log"
	"github.com/shalb/cluster.dev/internal/config"
	"gopkg.in/yaml.v2"
)

// Ident - string key for identify provider in the providers map.
const Ident = "aws"

// aws provider sub-config.
type providerConfSpec struct {
	Region            string                 `yaml:"region"`
	Vpc               string                 `yaml:"vpc"`
	Domain            string                 `yaml:"domain"`
	Provisioner       map[string]interface{} `yaml:"provisioner"`
	ProviderType      string                 `yaml:"type"`
	AvailabilityZones []string               `yaml:"availability_zones"`
	ClusterName       string                 `yaml:"cluster_name"`
}

// ModuleCommon - interface for terraform modules instance.
type ModuleCommon interface {
	// Apply module with his defined configuration.
	Deploy() error
	// Destroy infrastructure, created by module.
	Destroy() error
	// Some modules checks.
	Check() (bool, error)
}

// Provider - main provider object.
type Provider struct {
	Config     providerConfSpec
	kubeConfig string
}

// Init provider, check config.
func (p *Provider) Init(yamlSpec []byte, clusterName string) error {
	var spec providerConfSpec
	err := yaml.UnmarshalStrict(yamlSpec, &spec)
	if err != nil {
		return err
	}

	log.Debugf("terraform root dir is set to dir is '%s'", config.Global.ProjectRoot)
	p.Config = spec
	p.Config.ClusterName = clusterName
	return nil
}

// Deploy function.
func (p *Provider) Deploy() error {
	// Create bucket.
	log.Info("Deploying backend bucket...")
	backend, err := NewS3Backend(p.Config)
	if err != nil {
		return err
	}

	exists, err := backend.Check()
	if err != nil {
		return err
	}

	if !exists {
		err = backend.Deploy()
		if err != nil {
			return err
		}
	}

	// Deploy DNS.
	log.Info("Deploying Route53...")
	route53, err := NewRoute53(p.Config)
	if err != nil {
		return err
	}
	err = route53.Deploy()
	if err != nil {
		return err
	}

	// Deploy VPC.
	log.Info("Deploying VPC...")
	vpc, err := NewVpc(p.Config)
	if err != nil {
		return err
	}
	err = vpc.Deploy()
	if err != nil {
		return err
	}
	// Deploy EKS
	provisioner, err := NewProvisioner(p.Config)
	if err != nil {

		return err
	}
	log.Info("Deploying provisioner...")
	err = provisioner.Deploy(time.Minute * 10) // Timeout - 10 min.
	if err != nil {
		return err
	}
	p.kubeConfig = provisioner.GetKubeConfig()
	if p.kubeConfig == "" {
		return fmt.Errorf("provisioner kube config is empty, can't deploy addons")
	}

	log.Debugf("Kubernetes config: \n %s", p.kubeConfig)

	log.Info("Deploying addons...")
	// Create addons instance.
	addons, err := NewAddons(p.Config, p.kubeConfig)
	if err != nil {
		return err
	}
	// Deploy addons.
	err = addons.Deploy()
	if err != nil {
		return err
	}
	return nil
}

// Destroy function.
func (p *Provider) Destroy() error {

	log.Debug("Check if backend bucket exists")

	backend, err := NewS3Backend(p.Config)
	if err != nil {
		return err
	}
	log.Info("Checking backend...")
	exists, err := backend.Check()
	if err != nil {
		return err
	}

	if !exists {
		log.Infof("Backend bucket '%s' is not found. Nothing to destroy.", p.Config.ClusterName)
		return nil
	}

	// Create provisioner instance.
	provisioner, err := NewProvisioner(p.Config)
	if err != nil {
		return err
	}
	// Pull kubernetes config from s3 to ~/.kube/.
	err = provisioner.PullKubeConfig()
	if err == nil {
		p.kubeConfig = provisioner.GetKubeConfig()
		// Create new addons instance.
		addons, err := NewAddons(p.Config, p.kubeConfig)
		if err != nil {
			return err
		}
		// Deploy addons.
		log.Info("Destroying addons...")
		err = addons.Destroy()
		if err != nil {
			return err
		}
	} else {
		log.Info("KubeConfig is not found in bucket, ignore addons destroying.")
	}

	log.Info("Destroying provisioner...")
	err = provisioner.Destroy()
	if err != nil {
		return err
	}

	// Destroy VPC.
	vpc, err := NewVpc(p.Config)
	if err != nil {
		return err
	}
	log.Info("Destroying VPC...")
	err = vpc.Destroy()
	if err != nil {
		return err
	}
	// Destroy DNS.
	route53, err := NewRoute53(p.Config)
	if err != nil {
		return err
	}
	log.Info("Destroying Route53...")
	err = route53.Destroy()
	if err != nil {
		return err
	}
	log.Info("Destroying backend...")
	// Remove bucket and dynamodb table.
	err = backend.Destroy()
	if err != nil {
		return err
	}

	return nil
}

// GetKubeConfig return kube config as string.
func (p *Provider) GetKubeConfig() string {
	return p.kubeConfig
}
