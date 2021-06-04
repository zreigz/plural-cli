package api

import (
	"fmt"
)

const loginQuery = `
	mutation Login($email: String!, $pwd: String!) {
		login(email: $email, password: $pwd) {
			jwt
		}
	}
`

const impersonationQuery = `
	mutation Impersonate($email: String) {
		impersonateServiceAccount(email: $email) { 
			jwt 
			email
		}
	}
`

const createTokenQuery = `
	mutation {
		createToken {
			token
		}
	}
`

const listTokenQuery = `
	query {
		tokens(first: 3) {
			edges {
				node {
					token
				}
			}
		}
	}
`

const createUpgradeMut = `
	mutation Upgrade($name: String, $attributes: UpgradeAttributes!) {
		createUpgrade(name: $name, attributes: $attributes) {
			id
		}
	} 
`

var listKeys = fmt.Sprintf(`
	query ListKeys($emails: [String]) {
		publicKeys(emails: $emails, first: 1000) {
			edges { node { ...PublicKeyFragment } }
		}
	}
	%s
`, PublicKeyFragment)

const createKey = `
	mutation Create($key: String!, $name: String!) {
		createPublicKey(attributes: {content: $key, name: $name}) { id }
	}
`

type UpgradeAttributes struct {
	Message string
}

type login struct {
	Login struct {
		Jwt string `json:"jwt"`
	}
	ImpersonateServiceAccount struct {
		Jwt string `json:"jwt"`
		Email string `json:"email"`
	}
}

type createToken struct {
	CreateToken struct {
		Token string
	}
}

type listToken struct {
	Tokens struct {
		Edges []struct {
			Node struct {
				Token string
			}
		}
	}
}


func (client *Client) Login(email, pwd string) (string, error) {
	var resp login
	req := client.Build(loginQuery)
	req.Var("email", email)
	req.Var("pwd", pwd)
	err := client.Run(req, &resp)
	return resp.Login.Jwt, err
}

func (client *Client) ImpersonateServiceAccount(email string) (string, string, error) {
	var resp login
	req := client.Build(loginQuery)
	req.Var("email", email)
	err := client.Run(req, &resp)
	return resp.ImpersonateServiceAccount.Jwt, resp.ImpersonateServiceAccount.Email, err
}

func (client *Client) CreateAccessToken() (string, error) {
	var resp createToken
	req := client.Build(createTokenQuery)
	err := client.Run(req, &resp)
	return resp.CreateToken.Token, err
}

func (client *Client) GrabAccessToken() (string, error) {
	var resp listToken
	req := client.Build(listTokenQuery)
	err := client.Run(req, &resp)
	if err != nil {
		return "", err
	}
	if len(resp.Tokens.Edges) > 0 {
		return resp.Tokens.Edges[0].Node.Token, nil
	}

	return client.CreateAccessToken()
}

func (client *Client) CreateUpgrade(name string, message string) (id string, err error) {
	var resp struct {
		CreateUpgrade *Upgrade
	}

	req := client.Build(createUpgradeMut)
	req.Var("name", name)
	req.Var("attributes", UpgradeAttributes{Message: message})
	err = client.Run(req, &resp)
	if err == nil {
		id = resp.CreateUpgrade.Id
	}

	return
}

func (client *Client) ListKeys(emails []string) (keys []*PublicKey, err error) {
	var resp struct {
		PublicKeys struct {
			Edges []*PublicKeyEdge
		}
	}

	req := client.Build(listKeys)
	req.Var("emails", emails)
	err  = client.Run(req, &resp)
	keys = []*PublicKey{}
	for _, edge := range resp.PublicKeys.Edges {
		keys = append(keys, edge.Node)
	}
	return
}

func (client *Client) CreateKey(name, content string) error {
	var resp struct {
		CreatePublicKey struct {
			Id string
		}
	}

	req := client.Build(createKey)
	req.Var("key", content)
	req.Var("name", name)
	return client.Run(req, &resp)
}