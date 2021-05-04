package provider

import (
	"fmt"
	"os"
	"context"
	"os/exec"
	"net/url"
	"encoding/json"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2019-06-01/storage"
	"github.com/pluralsh/plural/pkg/manifest"
	"github.com/pluralsh/plural/pkg/template"
	"github.com/pluralsh/plural/pkg/utils"
)

type AzureProvider struct {
	cluster        string
	resourceGroup  string
	bucket         string
	region         string
	ctx 			     map[string]interface{}
}

const azureBackendTemplate = `terraform {
	backend "azurerm" {
		storage_account_name = {{ .Values.Context.StorageAccount | quote }}
		container_name = {{ .Values.Bucket | quote }}
		key = "{{ .Values.__CLUSTER__ }}/{{ .Values.Prefix }}/terraform.tfstate"
	}

	required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
      version = "2.57.0"
    }
		kubernetes = {
			source  = "hashicorp/kubernetes"
			version = "~> 2.0.3"
		}
  }
}

provider "azurerm" {
  features {}
}

data "azurerm_kubernetes_cluster" "cluster" {
  name = {{ .Values.Cluster | quote }}
	resource_group_name = {{ .Values.Project | quote }}
}

provider "kubernetes" {
  host                   = azurerm_kubernetes_cluster.host
  client_certificate     = base64decode(azurerm_kubernetes_cluster.client_certificate)
  client_key             = base64decode(azurerm_kubernetes_cluster.client_key)
  cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.cluster_ca_certificate)
}
`

func mkAzure() (prov *AzureProvider, err error) {
	cluster, _ := utils.ReadLine("Enter the name of your cluster: ")
	storAcct, _ := utils.ReadLine("Enter the name of the storage account to use for your stage, must be globally unique or owned by your subscription: ")
	bucket, _ := utils.ReadLine("Enter the name of a storage container to use for state, eg: <yourprojectname>-tf-state: ")
	region, _ := utils.ReadLine("Enter the region you want to deploy to eg US East: ")
	rg, _ := utils.ReadLine("Enter the name of the resource group to use as default: ")

	subId, tenID, err := getAzureAccount()
	if err != nil {
		return
	}

	prov = &AzureProvider{
		cluster,
		rg,
		bucket,
		region,
		map[string]interface{}{
			"SubscriptionId": subId,
			"TenantId": tenID,
			"StorageAccount": storAcct,
		},
	}

	projectManifest := manifest.ProjectManifest{
		Cluster:  cluster,
		Project:  rg,
		Bucket:   bucket,
		Provider: AZURE,
		Region:   prov.Region(),
		Context:  prov.Context(),
	}
	err = projectManifest.Write(manifest.ProjectManifestPath())
	return
}

func azureFromManifest(man *manifest.Manifest) (*AzureProvider, error) {
	return &AzureProvider{man.Cluster, man.Project, man.Bucket, man.Region, man.Context}, nil
}

func (azure *AzureProvider) CreateBackend(prefix string, ctx map[string]interface{}) (string, error) {
	if err := azure.CreateBucket(azure.bucket); err != nil {
		return "", err
	}

	ctx["Region"] = azure.Region()
	ctx["Bucket"] = azure.Bucket()
	ctx["Prefix"] = prefix
	ctx["Project"] = azure.Project()
	ctx["__CLUSTER__"] = azure.Cluster()
	if _, ok := ctx["Cluster"]; !ok {
		ctx["Cluster"] = fmt.Sprintf("\"%s\"", azure.Cluster())
	}

	return template.RenderString(azureBackendTemplate, ctx)
}

func (az *AzureProvider) CreateBucket(bucket string) (err error) {
	acc, err := az.upsertStorageAccount(utils.ToString(az.Context()["StorageAccount"]))
	if err != nil {
		return
	}

	err = az.upsertStorageContainer(acc, bucket)
	if err != nil {
		return
	}
	return
}

func (azure *AzureProvider) KubeConfig() error {
	if utils.InKubernetes() {
		return nil
	}

	cmd := exec.Command(
		"az", "eks", "get-credentials", "--name", azure.cluster, "--resource-group", azure.resourceGroup)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (azure *AzureProvider) Install() (err error) {
	if exists, _ := utils.Which("az"); exists {
		utils.Success("azure cli already installed!\n")
		return
	}

	fmt.Println("visit https://docs.microsoft.com/en-us/cli/azure/install-azure-cli to install")
	return
}

func (az *AzureProvider) Name() string {
	return AZURE
}

func (az *AzureProvider) Cluster() string {
	return az.cluster
}

func (az *AzureProvider) Project() string {
	return az.resourceGroup
}

func (az *AzureProvider) Bucket() string {
	return az.bucket
}

func (az *AzureProvider) Region() string {
	return az.region
}

func (az *AzureProvider) Context() map[string]interface{} {
	return az.ctx
}

func (az *AzureProvider) Authorizer() (autorest.Authorizer, error) {
	if (os.Getenv("ARM_USE_MSI") != "") {
		return auth.NewAuthorizerFromEnvironment()
	}

	return auth.NewAuthorizerFromCLI()
 }

 func (az *AzureProvider) getStorageAccountsClient() storage.AccountsClient {
	 storageAccountsClient := storage.NewAccountsClient(utils.ToString(az.ctx["SubscriptionId"]))
	 auth, _ := az.Authorizer()
	 storageAccountsClient.Authorizer = auth
	return storageAccountsClient
}

func (az *AzureProvider) getStorageAccount(account string) (storage.Account, error) {
	client := az.getStorageAccountsClient()
	return client.GetProperties(context.Background(), az.resourceGroup, account, storage.AccountExpandBlobRestoreStatus)
}

func (az *AzureProvider) upsertStorageAccount(account string) (acc storage.Account, err error) {
	acc, err = az.getStorageAccount(account)
	if err == nil {
		return
	}

	client := az.getStorageAccountsClient()
	ctx := context.Background()
	future, err := client.Create(
		ctx,
		az.resourceGroup,
		account,
		storage.AccountCreateParameters{
			Sku: &storage.Sku{Name: storage.StandardLRS},
			Kind:                              storage.StorageV2,
			Location:                          to.StringPtr(az.region),
			AccountPropertiesCreateParameters: &storage.AccountPropertiesCreateParameters{},
		})

	if err != nil {
		return
	}

	err = future.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		return
	}

	acc, err = future.Result(client)
	return
}

func (az *AzureProvider) upsertStorageContainer(acc storage.Account, name string) error {
	ctx := context.Background()
	accountName := *acc.Name

	client := az.getStorageAccountsClient()
	resp, err := client.ListKeys(ctx, az.resourceGroup, accountName, storage.Kerb)
	if err != nil {
		return err
	}
	key := *(((*resp.Keys)[0]).Value)

	c, _ := azblob.NewSharedKeyCredential(accountName, key)
	p := azblob.NewPipeline(c, azblob.PipelineOptions{})
	u, _ := url.Parse(fmt.Sprintf(`https://%s.blob.core.windows.net`, accountName))
	service := azblob.NewServiceURL(*u, p)

	container := service.NewContainerURL(name)
	_, err = container.Create(ctx, azblob.Metadata{}, azblob.PublicAccessContainer)
	return err
}

func getAzureAccount() (string, string, error) {
	cmd := exec.Command("az", "account", "show")
	out, err := cmd.Output()
	if err != nil {
		fmt.Println(out)
		return "", "", err
	}

	var res struct {
		TenantId string
		Id string
	}

	json.Unmarshal(out, &res)
	return res.Id, res.TenantId, nil
}