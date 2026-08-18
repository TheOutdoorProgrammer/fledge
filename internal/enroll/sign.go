package enroll

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/smallstep/pkcs7"
)

// Sign wraps a configuration profile in a CMS envelope, which is what stops iOS
// labelling it "Not Signed". Any certificate the device already trusts will do,
// Apple-issued or otherwise.
func Sign(profile, certPEM, keyPEM []byte) ([]byte, error) {
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, errors.New("enroll: a certificate and key are required to sign")
	}

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("enroll: load signing pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("enroll: signing certificate is empty")
	}

	leaf := pair.Leaf
	if leaf == nil {
		leaf, err = parseLeaf(pair.Certificate[0])
		if err != nil {
			return nil, err
		}
	}

	signed, err := pkcs7.NewSignedData(profile)
	if err != nil {
		return nil, fmt.Errorf("enroll: start signature: %w", err)
	}
	if err := signed.AddSigner(leaf, pair.PrivateKey, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("enroll: add signer: %w", err)
	}

	// Intermediates travel with the profile, since the device has no other way
	// to build a path from the leaf to a root it trusts.
	for _, intermediate := range pair.Certificate[1:] {
		certificate, err := parseLeaf(intermediate)
		if err != nil {
			return nil, err
		}
		signed.AddCertificate(certificate)
	}

	envelope, err := signed.Finish()
	if err != nil {
		return nil, fmt.Errorf("enroll: finish signature: %w", err)
	}

	return envelope, nil
}

func parseLeaf(der []byte) (*x509.Certificate, error) {
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("enroll: parse signing certificate: %w", err)
	}
	return certificate, nil
}
