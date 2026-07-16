package tls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

func TestCertificateSHA256Pin(t *testing.T) {
	rawCertificate := []byte("test leaf certificate DER")
	hashValue := sha256.Sum256(rawCertificate)
	pinnedValue := strings.ToUpper(hex.EncodeToString(hashValue[:]))
	pinParts := splitEveryTwo(pinnedValue)
	pinnedValue = strings.Join(pinParts, ":")

	knownHashValues, err := ParseCertificateSHA256([]string{pinnedValue, strings.Join(pinParts, "-")})
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyCertificateSHA256(knownHashValues, [][]byte{rawCertificate}); err != nil {
		t.Fatal(err)
	}
	if err = VerifyCertificateSHA256(knownHashValues, [][]byte{[]byte("other certificate")}); err == nil {
		t.Fatal("expected certificate pin mismatch")
	}
	if err = VerifyCertificateSHA256(knownHashValues, nil); err == nil {
		t.Fatal("expected missing certificate error")
	}
}

func TestCertificateSHA256OptionValidation(t *testing.T) {
	hashValue := sha256.Sum256([]byte("certificate"))
	validPin := hex.EncodeToString(hashValue[:])
	var decodedOptions option.OutboundTLSOptions
	if err := json.Unmarshal([]byte(`{"enabled":true,"certificate_sha256":["`+validPin+`"]}`), &decodedOptions); err != nil {
		t.Fatal(err)
	}
	if len(decodedOptions.CertificateSHA256) != 1 || decodedOptions.CertificateSHA256[0] != validPin {
		t.Fatalf("unexpected decoded certificate pin: %+v", decodedOptions.CertificateSHA256)
	}

	config, err := newSTDClient(context.Background(), logger.NOP(), "localhost", option.OutboundTLSOptions{
		Enabled:           true,
		Insecure:          true,
		CertificateSHA256: []string{validPin},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	stdConfig := config.(*STDClientConfig).config
	if !stdConfig.InsecureSkipVerify || stdConfig.VerifyPeerCertificate == nil {
		t.Fatal("insecure certificate pin must skip CA validation but retain pin validation")
	}
	if err = stdConfig.VerifyPeerCertificate([][]byte{[]byte("certificate")}, nil); err != nil {
		t.Fatal(err)
	}
	config, err = newSTDClient(context.Background(), logger.NOP(), "localhost", option.OutboundTLSOptions{
		Enabled:           true,
		CertificateSHA256: []string{validPin},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	stdConfig = config.(*STDClientConfig).config
	if stdConfig.InsecureSkipVerify || stdConfig.VerifyPeerCertificate == nil {
		t.Fatal("certificate pin without insecure must retain normal CA validation")
	}

	_, err = newSTDClient(context.Background(), logger.NOP(), "localhost", option.OutboundTLSOptions{
		Enabled:           true,
		CertificateSHA256: []string{"invalid"},
	}, false)
	if err == nil {
		t.Fatal("expected invalid certificate pin error")
	}
	_, err = newSTDClient(context.Background(), logger.NOP(), "localhost", option.OutboundTLSOptions{
		Enabled:                    true,
		CertificateSHA256:          []string{validPin},
		CertificatePublicKeySHA256: [][]byte{hashValue[:]},
	}, false)
	if err == nil {
		t.Fatal("expected certificate and public-key pin conflict")
	}
}

func TestCertificateSHA256WithCustomCA(t *testing.T) {
	certificateDER, certificatePEM := newCertificatePinTestCA(t)
	hashValue := sha256.Sum256(certificateDER)
	config, err := newSTDClient(context.Background(), logger.NOP(), "localhost", option.OutboundTLSOptions{
		Enabled:           true,
		Certificate:       []string{certificatePEM},
		CertificateSHA256: []string{hex.EncodeToString(hashValue[:])},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	stdConfig := config.(*STDClientConfig).config
	if stdConfig.InsecureSkipVerify || stdConfig.RootCAs == nil || stdConfig.VerifyPeerCertificate == nil {
		t.Fatal("custom CA validation and certificate pin must both remain enabled")
	}
	if err = stdConfig.VerifyPeerCertificate([][]byte{certificateDER}, nil); err != nil {
		t.Fatal(err)
	}
	if err = stdConfig.VerifyPeerCertificate([][]byte{[]byte("other certificate")}, nil); err == nil {
		t.Fatal("expected certificate pin mismatch with custom CA")
	}
}

func newCertificatePinTestCA(t *testing.T) ([]byte, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	return certificateDER, string(certificatePEM)
}

func splitEveryTwo(value string) []string {
	parts := make([]string, 0, len(value)/2)
	for index := 0; index < len(value); index += 2 {
		parts = append(parts, value[index:index+2])
	}
	return parts
}
