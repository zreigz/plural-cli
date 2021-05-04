package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"runtime"
	"path/filepath"
	"github.com/mholt/archiver/v3"

	"cloud.google.com/go/storage"
	"github.com/pluralsh/plural/pkg/manifest"
	"github.com/pluralsh/plural/pkg/template"
	"github.com/pluralsh/plural/pkg/utils"
)

type GCPProvider struct {
	cluster       string
	project       string
	bucket        string
	region        string
	storageClient *storage.Client
	ctx           context.Context
}

const backendTemplate = `terraform {
	backend "gcs" {
		bucket = {{ .Values.Bucket | quote }}
		prefix = "{{ .Values.__CLUSTER__ }}/{{ .Values.Prefix }}"
	}

	required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 3.65.0"
    }
		kubernetes = {
			source  = "hashicorp/kubernetes"
			version = "~> 2.0.3"
		}
  }
}

locals {
	gcp_location  = {{ .Values.Location | quote }}
  gcp_location_parts = split("-", local.gcp_location)
  gcp_region         = "${local.gcp_location_parts[0]}-${local.gcp_location_parts[1]}"
}

provider "google" {
  project = {{ .Values.Project | quote }}
  region  = local.gcp_region
}

data "google_client_config" "current" {}

{{ if .Values.ClusterCreated }}
provider "kubernetes" {
  host = {{ .Values.Cluster }}.endpoint
  cluster_ca_certificate = base64decode({{ .Values.Cluster }}.ca_certificate)
  token = data.google_client_config.current.access_token
}
{{ else }}
data "google_container_cluster" "cluster" {
  name = {{ .Values.Cluster }}
  location = local.gcp_region
}

provider "kubernetes" {
  host = data.google_container_cluster.cluster.endpoint
  cluster_ca_certificate = base64decode(data.google_container_cluster.cluster.master_auth.0.cluster_ca_certificate)
  token = data.google_client_config.current.access_token
}
{{ end }}
`

func mkGCP() (*GCPProvider, error) {
	client, ctx, err := storageClient()
	if err != nil {
		return nil, err
	}
	cluster, _ := utils.ReadLine("Enter the name of your cluster: ")
	project, _ := utils.ReadLine("Enter the name of its gcp project: ")
	bucket, _ := utils.ReadLine("Enter the name of a gcs bucket to use for state, eg: <yourprojectname>-tf-state: ")
	provider := &GCPProvider{
		cluster,
		project,
		bucket,
		getRegion(),
		client,
		ctx,
	}
	projectManifest := manifest.ProjectManifest{
		Cluster:  cluster,
		Project:  project,
		Bucket:   bucket,
		Provider: GCP,
		Region:   provider.Region(),
	}
	path := manifest.ProjectManifestPath()
	projectManifest.Write(path)

	return provider, nil
}

func storageClient() (*storage.Client, context.Context, error) {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	return client, ctx, err
}

func gcpFromManifest(man *manifest.Manifest) (*GCPProvider, error) {
	client, ctx, err := storageClient()
	if err != nil {
		return nil, err
	}

	region := man.Region
	if region == "" {
		region = "us-east1-b"
	}

	return &GCPProvider{man.Cluster, man.Project, man.Bucket, region, client, ctx}, nil
}

func (gcp *GCPProvider) KubeConfig() error {
	if utils.InKubernetes() {
		return nil
	}

	// move tf supported env var to gcloud's
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", os.Getenv("GOOGLE_CREDENTIALS"))
	cmd := exec.Command(
		"gcloud", "container", "clusters", "get-credentials", gcp.cluster,
		"--region", getZone(gcp.region), "--project", gcp.project)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (gcp *GCPProvider) CreateBackend(prefix string, ctx map[string]interface{}) (string, error) {
	if err := gcp.mkBucket(gcp.bucket); err != nil {
		return "", err
	}

	ctx["Project"] = gcp.Project()
	ctx["Location"] = gcp.Region()
	ctx["Bucket"] = gcp.Bucket()
	ctx["Prefix"] = prefix
	ctx["ClusterCreated"] = false
	ctx["__CLUSTER__"] = gcp.Cluster()
	if cluster, ok := ctx["cluster"]; ok {
		ctx["Cluster"] = cluster
		ctx["ClusterCreated"] = true
	} else {
		ctx["Cluster"] = fmt.Sprintf(`"%s"`, gcp.Cluster())
	}
	return template.RenderString(backendTemplate, ctx)
}

func (gcp *GCPProvider) mkBucket(name string) error {
	bkt := gcp.storageClient.Bucket(name)
	if _, err := bkt.Attrs(gcp.ctx); err != nil {
		return bkt.Create(gcp.ctx, gcp.project, nil)
	}
	return nil
}

func getRegion() string {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", os.Getenv("GOOGLE_CREDENTIALS"))
	cmd := exec.Command("gcloud", "config", "get-value", "compute/zone")
	res, err := cmd.CombinedOutput()
	if err != nil {
		return "us-east1-b"
	}

	return strings.Split(string(res), "\n")[1]
}

func getZone(region string) string {
	split := strings.Split(region, "-")
	return strings.Join(split[:2], "-")
}

func (gcp *GCPProvider) Install() (err error) {
	if exists, _ := utils.Which("gcloud"); exists {
		utils.Success("gcloud already installed!\n")
		return
	}

	goos := runtime.GOOS 
	arch := runtime.GOARCH
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
		break;
	case "arm64":
		arch = "arm"
	}

	url := fmt.Sprintf("https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-335.0.0-%s-%s.tar.gz", goos, arch)
	root, _ := utils.ProjectRoot()
	dest := filepath.Join(root, "gcloud-sdk.tar.gz")
	return utils.Install("gcloud", url, dest, func(dest string) (string, error) {
		gcloudPath := filepath.Join(filepath.Dir(dest), "gcloud-sdk")
		err := archiver.Unarchive(dest, gcloudPath)
		if err != nil {
		  return "", err
		}

		installCommand := "install.sh"
		if goos == "windows" {
			installCommand = "install.bat"
		}

		cmd := exec.Command(filepath.Join(gcloudPath, installCommand), "--quiet")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return "", cmd.Run()
	})
}

func (gcp *GCPProvider) Name() string {
	return GCP
}

func (gcp *GCPProvider) Cluster() string {
	return gcp.cluster
}

func (gcp *GCPProvider) Project() string {
	return gcp.project
}

func (gcp *GCPProvider) Bucket() string {
	return gcp.bucket
}

func (gcp *GCPProvider) Region() string {
	return gcp.region
}

func (gcp *GCPProvider) Context() map[string]interface{} {
	return map[string]interface{}{}
}