package user

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"time"

	certificates "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// certIssuer owns certificate material and the Kubernetes CSR lifecycle: key
// generation, CSR creation, submission, approval, and retrieval. It is the one
// place that changes when certificate handling changes (SRP).
type certIssuer struct {
	clientset kubernetes.Interface
}

func newCertIssuer(clientset kubernetes.Interface) *certIssuer {
	return &certIssuer{clientset: clientset}
}

// GenerateKey creates a 2048-bit RSA private key.
func (c *certIssuer) GenerateKey() (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return key, nil
}

// CreateCSR builds a PEM-encoded certificate signing request for username, with
// any groups carried as certificate Organizations.
func (c *certIssuer) CreateCSR(key *rsa.PrivateKey, username string, groups []string) ([]byte, error) {
	subject := pkix.Name{
		CommonName: username,
	}
	if len(groups) > 0 {
		subject.Organization = groups
	}

	template := &x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	}), nil
}

// MaxCertExpirationDays caps a certificate's requested lifetime. It keeps the
// days→seconds→int32 conversion in Submit from overflowing (3650 days ≈ 10y, far
// below the int32 second ceiling of ~24855 days) and rejects nonsensical/negative
// values before they reach the Kubernetes CSR API.
const MaxCertExpirationDays = 3650

// Submit (re)creates the CSR object in the cluster, replacing any existing one
// with the same name.
func (c *certIssuer) Submit(name string, csrPEM []byte, expirationDays int) error {
	ctx, cancel := apiCtx()
	defer cancel()

	// Defensive bound (the CLI also validates): guarantees the int32 conversion
	// below cannot overflow or go negative regardless of caller.
	if expirationDays < 1 || expirationDays > MaxCertExpirationDays {
		return fmt.Errorf("expiration %d out of range (must be 1..%d days)", expirationDays, MaxCertExpirationDays)
	}

	// Delete existing CSR if it exists
	_ = c.clientset.CertificatesV1().CertificateSigningRequests().Delete(
		ctx, name, metav1.DeleteOptions{})

	expirationSeconds := expirationDays * 24 * 60 * 60 // days to seconds
	// Bounded above by MaxCertExpirationDays, so this conversion cannot overflow. #nosec G115
	expiration := int32(expirationSeconds)

	csr := &certificates.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: certificates.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        "kubernetes.io/kube-apiserver-client",
			ExpirationSeconds: &expiration,
			Usages: []certificates.KeyUsage{
				certificates.UsageClientAuth,
			},
		},
	}

	_, err := c.clientset.CertificatesV1().CertificateSigningRequests().Create(
		ctx, csr, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to submit CSR: %w", err)
	}

	return nil
}

// Approve approves a submitted CSR so the control plane signs it.
func (c *certIssuer) Approve(name string) error {
	ctx, cancel := apiCtx()
	defer cancel()

	csr, err := c.clientset.CertificatesV1().CertificateSigningRequests().Get(
		ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CSR: %w", err)
	}

	csr.Status.Conditions = append(csr.Status.Conditions, certificates.CertificateSigningRequestCondition{
		Type:           certificates.CertificateApproved,
		Status:         "True",
		Reason:         "ApprovedByK8sProvisioner",
		Message:        "Approved by k8s-provisioner user command",
		LastUpdateTime: metav1.Now(),
	})

	_, err = c.clientset.CertificatesV1().CertificateSigningRequests().UpdateApproval(
		ctx, name, csr, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to approve CSR: %w", err)
	}

	return nil
}

// WaitForCertificate polls until the signed certificate is available or timeout.
func (c *certIssuer) WaitForCertificate(name string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		ctx, cancel := apiCtx()
		csr, err := c.clientset.CertificatesV1().CertificateSigningRequests().Get(
			ctx, name, metav1.GetOptions{})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("failed to get CSR: %w", err)
		}

		if len(csr.Status.Certificate) > 0 {
			return csr.Status.Certificate, nil
		}

		time.Sleep(certificatePollInterval)
	}

	return nil, fmt.Errorf("timeout waiting for certificate")
}

// Delete removes the CSR object from the cluster. A NotFound result is treated
// as success (the object is already gone); other errors are returned so callers
// can report cleanup failures.
func (c *certIssuer) Delete(name string) error {
	ctx, cancel := apiCtx()
	defer cancel()

	err := c.clientset.CertificatesV1().CertificateSigningRequests().Delete(
		ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting CSR %s: %w", name, err)
	}
	return nil
}

// encodePrivateKeyPEM returns the PKCS#1 PEM encoding of an RSA private key.
func encodePrivateKeyPEM(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}
