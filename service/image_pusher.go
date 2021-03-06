package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/jsonmessage"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/versions"
	dclient "github.com/docker/docker/client"
	"github.com/rancher/go-rancher/v3"
	"golang.org/x/net/context"
)

const DEFAULT_REGISTRY = "index.docker.io"

//AuthAndPush find registry credential and push the image
func AuthAndPush(apiClient *client.RancherClient, image string) error {
	username, password, err := getRegistryAuth(apiClient, image)
	if err != nil {
		return err
	}
	//logrus.Debugf("get auth:%v,%v,%v", image, username, password)
	return pushImage(image, username, password)
}

func getRegistryAuth(apiClient *client.RancherClient, image string) (string, string, error) {
	opt := &client.ListOpts{}
	regCollection, err := apiClient.Registry.List(opt)
	if err != nil {
		return "", "", err
	}

	hostName, _ := splitHostName(image)
	var regToPush *client.Registry
	for _, reg := range regCollection.Data {
		if reg.ServerAddress == hostName {
			regToPush = &reg
			break
		}
	}
	username, password := "", ""
	if regToPush == nil {
		logrus.Warningf("Cannot find registry credential for '%v', You probably need to add it in registries configuration.", image)
	} else {

		var regCredToPush *client.RegistryCredential
		regCredCollection, err := apiClient.RegistryCredential.List(opt)
		if err != nil {
			return "", "", err
		}
		for _, regCred := range regCredCollection.Data {
			if regCred.RegistryId == regToPush.Id {
				regCredToPush = &regCred
				break
			}
		}
		if regCredToPush == nil {
			logrus.Warningf("Cannot find registry credential for '%v', You probably need to add it in registries configuration.", image)
		} else {
			username = regCredToPush.PublicValue
			password = regCredToPush.SecretValue
		}
	}
	return username, password, nil

}

func pushImage(image string, username string, password string) error {
	ctx := context.Background()
	cli, err := dclient.NewEnvClient()
	if err != nil {
		return err
	}
	if err := backwardVersion(cli); err != nil {
		return err
	}
	authConfig := types.AuthConfig{
		Username: username,
		Password: password,
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		panic(err)
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)
	out, err := cli.ImagePush(ctx, image, types.ImagePushOptions{RegistryAuth: authStr})
	if err != nil {
		return err
	}

	defer out.Close()
	dec := json.NewDecoder(out)
	for {
		var message jsonmessage.JSONMessage
		if err := dec.Decode(&message); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		//Do not print progress message
		if message.ProgressMessage == "" && message.Status != "" {
			msg := message.Status
			if message.ID != "" {
				msg = message.ID + ": " + msg
			}
			logrus.Infoln(msg)
		}
		if message.Error != nil {
			logrus.Errorln(message.ErrorMessage)
			return fmt.Errorf("Push image '%s' FAIL", image)
		}
	}
	logrus.Infof("Push image '%s' SUCCESS", image)
	return nil
}

func backwardVersion(cli *dclient.Client) error {
	ping, err := cli.Ping(context.Background())
	if err != nil {
		return err
	}
	// since the new header was added in 1.25, assume server is 1.24 if header is not present.
	if ping.APIVersion == "" {
		ping.APIVersion = "1.24"
	}

	// if server version is lower than the current cli, downgrade
	if versions.LessThan(ping.APIVersion, cli.ClientVersion()) {
		cli.UpdateClientVersion(ping.APIVersion)
	}
	return nil
}

// encodeAuthToBase64 serializes the auth configuration as JSON base64 payload
func encodeAuthToBase64(authConfig types.AuthConfig) (string, error) {
	buf, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(buf), nil
}
func splitHostName(image string) (string, string) {
	i := strings.Index(image, "/")
	if i == -1 || (!strings.ContainsAny(image[:i], ".:") && image[:i] != "localhost") {
		return DEFAULT_REGISTRY, image
	}
	return image[:i], image[i+1:]
}
