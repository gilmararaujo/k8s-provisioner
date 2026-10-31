package provisioner

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func selfSignedCA(t *testing.T, cn string) (pemData string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), der
}

func TestFingerprint_MatchesSHA256OfDER(t *testing.T) {
	pemData, der := selfSignedCA(t, "k8s-lab-ca")

	sum := sha256.Sum256(der)
	got := fingerprint(pemData)
	assert.Len(t, got, 32*3-1, "32 colon-separated hex pairs")
	assert.Equal(t, fmt.Sprintf("%02X:%02X", sum[0], sum[1]), got[:5])
}

func TestFingerprint_InvalidInput(t *testing.T) {
	assert.Equal(t, "(invalid PEM)", fingerprint("not a cert"))
}

func TestCommonName(t *testing.T) {
	pemData, _ := selfSignedCA(t, "k8s-lab Vault CA")
	assert.Equal(t, "k8s-lab Vault CA", commonName(pemData))
	assert.Empty(t, commonName("not a cert"))
}

func TestMacOSRemoveCA_DeletesEveryCopyByHash(t *testing.T) {
	cmd := macOSRemoveCA("k8s-lab Vault CA")
	assert.Contains(t, cmd, `find-certificate -a -Z -c "k8s-lab Vault CA" /Library/Keychains/System.keychain`, "all copies, quoted name")
	assert.Contains(t, cmd, "sudo security delete-certificate -Z {} /Library/Keychains/System.keychain")
}
