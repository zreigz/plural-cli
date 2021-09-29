package provider

import (
	"fmt"
	"github.com/pluralsh/plural/pkg/manifest"
	"github.com/pluralsh/plural/pkg/utils"
	"github.com/pluralsh/plural/pkg/config"
	"k8s.io/api/core/v1"
	"strconv"
	"strings"
)

type Provider interface {
	Name() string
	Cluster() string
	Project() string
	Region() string
	Bucket() string
	KubeConfig() error
	CreateBackend(prefix string, ctx map[string]interface{}) (string, error)
	Install() error
	Context() map[string]interface{}
	Decommision(node *v1.Node) error
}

func Bootstrap(manifestPath string, force bool) (Provider, error) {
	if utils.Exists(manifestPath) {
		man, err := manifest.Read(manifestPath)
		if err != nil {
			return nil, err
		}

		return FromManifest(man)
	}

	return Select(force)
}

func Select(force bool) (Provider, error) {
	available := []string{GCP, AWS, AZURE}
	path := manifest.ProjectManifestPath()
	if utils.Exists(path) {
		if project, err := manifest.ReadProject(path); err == nil {
			prov, err := FromManifest(&manifest.Manifest{
				Provider: project.Provider,
				Project: project.Project,
				Cluster: project.Cluster,
				Region: project.Region,
				Bucket: project.Bucket,
				Context: project.Context,
			})

			if force {
				return prov, err
			}

			line := fmt.Sprintf("Reuse existing manifest {provider: %s, cluster: %s, bucket: %s, region: %s} [(y)/n]:",
				project.Provider, project.Cluster, project.Bucket, project.Region)
			val, _ := utils.ReadLine(line)

			if val != "n" {
				return prov, err
			}
		}
	}

	fmt.Println("Select one of the following providers:")
	for i, name := range available {
		fmt.Printf("[%d] %s\n", i, name)
	}
	fmt.Println("")

	val, _ := utils.ReadLine("Your choice: ")
	i, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return nil, err
	}

	if i >= len(available) {
		return nil, fmt.Errorf("Invalid index, must be < %d", len(available))
	}

	utils.Success("Using provider %s\n", available[i])
	return New(available[i])
}

func FromManifest(man *manifest.Manifest) (Provider, error) {
	switch man.Provider {
	case GCP:
		return gcpFromManifest(man)
	case AWS:
		return awsFromManifest(man)
	case AZURE:
		return azureFromManifest(man)
	default:
		return nil, fmt.Errorf("Invalid provider name: %s", man.Provider)
	}
}

func New(provider string) (Provider, error) {
	conf := config.Read()
	switch provider {
	case GCP:
		return mkGCP(conf)
	case AWS:
		return mkAWS(conf)
	case AZURE:
		return mkAzure(conf)
	default:
		return nil, fmt.Errorf("Invalid provider name: %s", provider)
	}
}
