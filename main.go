package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
)

var GroupName = os.Getenv("GROUP_NAME")

func main() {
	if GroupName == "" {
		panic("GROUP_NAME must be specified")
	}
	cmd.RunWebhookServer(GroupName, &primeDNSSolver{})
}

// secretGetter reads a Secret; it is a field so tests can replace the Kubernetes client.
type secretGetter func(ctx context.Context, namespace, name string) (*corev1.Secret, error)

// primeDNSSolver answers DNS-01 challenges with the PrimeDNS endpoint /v1/dns/acme-challenge.
type primeDNSSolver struct {
	getSecret secretGetter
}

var _ webhook.Solver = (*primeDNSSolver)(nil)

// primeDNSConfig is the `config` block of the webhook solver in the Issuer.
type primeDNSConfig struct {
	// APIKeySecretRef points at the Secret that holds the PrimeDNS API key (scope acme:name:<record name>
	// for every name to issue, or dns:write).
	APIKeySecretRef cmmeta.SecretKeySelector `json:"apiKeySecretRef"`
	// APIURL overrides the API base URL (default https://api.primedns.net/v1).
	APIURL string `json:"apiUrl,omitempty"`
}

func (s *primeDNSSolver) Name() string {
	return "primedns"
}

// Present creates the TXT record. cert-manager gives the name it resolved after following a CNAME, so
// delegating _acme-challenge to a name in a PrimeDNS zone needs no extra setting here.
func (s *primeDNSSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	client, name, err := s.prepare(ch)
	if err != nil {
		klog.ErrorS(err, "present failed", "fqdn", ch.ResolvedFQDN)
		return err
	}
	if err := client.Present(context.Background(), name, ch.Key); err != nil {
		klog.ErrorS(err, "present failed", "name", name)
		return err
	}
	klog.InfoS("TXT record presented", "name", name)
	return nil
}

// CleanUp removes only the record with this challenge's value.
func (s *primeDNSSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	client, name, err := s.prepare(ch)
	if err != nil {
		klog.ErrorS(err, "cleanup failed", "fqdn", ch.ResolvedFQDN)
		return err
	}
	if err := client.CleanUp(context.Background(), name, ch.Key); err != nil {
		// A key that may not touch this name could not have created the record either (Present failed the same
		// way), so there is nothing to remove. Failing here would leave the challenge stuck forever.
		if isScopeError(err) {
			klog.InfoS("nothing to remove: the key may not write this name", "name", name, "err", err.Error())
			return nil
		}
		klog.ErrorS(err, "cleanup failed", "name", name)
		return err
	}
	klog.InfoS("TXT record removed", "name", name)
	return nil
}

func (s *primeDNSSolver) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}
	s.getSecret = func(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
		return cl.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return nil
}

func (s *primeDNSSolver) prepare(ch *v1alpha1.ChallengeRequest) (*apiClient, string, error) {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return nil, "", err
	}
	if cfg.APIKeySecretRef.Name == "" || cfg.APIKeySecretRef.Key == "" {
		return nil, "", fmt.Errorf("config.apiKeySecretRef.name and .key are required")
	}
	if s.getSecret == nil {
		return nil, "", fmt.Errorf("the solver is not initialized")
	}
	secret, err := s.getSecret(context.Background(), ch.ResourceNamespace, cfg.APIKeySecretRef.Name)
	if err != nil {
		return nil, "", fmt.Errorf("reading Secret %s/%s: %w", ch.ResourceNamespace, cfg.APIKeySecretRef.Name, err)
	}
	raw, ok := secret.Data[cfg.APIKeySecretRef.Key]
	if !ok {
		return nil, "", fmt.Errorf("Secret %s/%s has no key %q", ch.ResourceNamespace, cfg.APIKeySecretRef.Name, cfg.APIKeySecretRef.Key)
	}
	apiKey := strings.TrimSpace(string(raw))
	if apiKey == "" {
		return nil, "", fmt.Errorf("Secret %s/%s key %q is empty", ch.ResourceNamespace, cfg.APIKeySecretRef.Name, cfg.APIKeySecretRef.Key)
	}
	return newAPIClient(cfg.APIURL, apiKey), recordName(ch.ResolvedFQDN), nil
}

// recordName turns the FQDN cert-manager gives ("_acme-challenge.example.com.") into the name the API takes.
func recordName(fqdn string) string {
	return strings.ToLower(strings.TrimSuffix(fqdn, "."))
}

func loadConfig(cfgJSON *extapi.JSON) (primeDNSConfig, error) {
	cfg := primeDNSConfig{}
	if cfgJSON == nil {
		return cfg, nil
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("error decoding solver config: %w", err)
	}
	return cfg, nil
}
