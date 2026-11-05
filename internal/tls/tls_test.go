/*
Copyright 2026 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tls

import (
	"crypto/x509"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestCreateSelfSignedTLSCertificate(t *testing.T) {
	cert, err := CreateSelfSignedTLSCertificate(logr.Discard())
	if err != nil {
		t.Fatalf("failed to create self signed certificate: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("expected certificate chain to be non-empty")
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	if len(x509Cert.Subject.Organization) != 1 || x509Cert.Subject.Organization[0] != "Inference Ext" {
		t.Fatalf("unexpected organization: %v", x509Cert.Subject.Organization)
	}

	if !x509Cert.BasicConstraintsValid {
		t.Fatal("expected basic constraints to be valid")
	}

	if x509Cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Fatal("expected KeyUsageKeyEncipherment to be set")
	}
	if x509Cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("expected KeyUsageDigitalSignature to be set")
	}

	if len(x509Cert.ExtKeyUsage) != 1 || x509Cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("unexpected ext key usage: %v", x509Cert.ExtKeyUsage)
	}

	wantNotAfter := x509Cert.NotBefore.Add(time.Hour * 24 * 365 * 10)
	if x509Cert.NotAfter.Sub(wantNotAfter).Abs() > time.Minute {
		t.Fatalf("expected NotAfter around %v, got %v", wantNotAfter, x509Cert.NotAfter)
	}

	if cert.PrivateKey == nil {
		t.Fatal("expected private key to be set")
	}
}

func TestCreateSelfSignedTLSCertificate_UniqueSerialNumbers(t *testing.T) {
	certA, err := CreateSelfSignedTLSCertificate(logr.Discard())
	if err != nil {
		t.Fatalf("failed to create first certificate: %v", err)
	}
	certB, err := CreateSelfSignedTLSCertificate(logr.Discard())
	if err != nil {
		t.Fatalf("failed to create second certificate: %v", err)
	}

	x509A, err := x509.ParseCertificate(certA.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse first certificate: %v", err)
	}
	x509B, err := x509.ParseCertificate(certB.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse second certificate: %v", err)
	}

	if x509A.SerialNumber.Cmp(x509B.SerialNumber) == 0 {
		t.Fatal("expected distinct serial numbers across calls")
	}
}
