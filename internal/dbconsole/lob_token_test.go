package dbconsole

import (
	"testing"
	"time"
)

func TestSignedLOBTokenRoundTrip(t *testing.T) {
	keys := map[string]any{"ID": 101}
	pks := []string{"ID"}
	token, err := GenerateSignedLOBToken("src1", "scott", "HR", "EMPLOYEES", "RESUME", "CLOB", "AAAB12AADAAAAwPAAA", keys, pks, "sess-1", 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateSignedLOBToken failed: %v", err)
	}

	payload, ref, err := VerifySignedLOBToken(token)
	if err != nil {
		t.Fatalf("VerifySignedLOBToken failed: %v", err)
	}

	if payload.SourceID != "src1" || payload.Owner != "HR" || payload.Table != "EMPLOYEES" || payload.Column != "RESUME" {
		t.Fatalf("payload mismatch: %+v", payload)
	}
	if ref.RowID != "AAAB12AADAAAAwPAAA" || ref.UseRowID {
		t.Fatalf("ref mismatch: %+v", ref)
	}
}

func TestSignedLOBTokenTampering(t *testing.T) {
	token, err := GenerateSignedLOBToken("src1", "scott", "HR", "EMPLOYEES", "RESUME", "CLOB", "", nil, nil, "", 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with first character of signature
	tampered := "x" + token
	if _, _, err := VerifySignedLOBToken(tampered); err == nil {
		t.Fatal("expected signature verification failure for tampered token")
	}
}

func TestSignedLOBTokenExpired(t *testing.T) {
	token, err := GenerateSignedLOBToken("src1", "scott", "HR", "EMPLOYEES", "RESUME", "CLOB", "", nil, nil, "", -1*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := VerifySignedLOBToken(token); err == nil {
		t.Fatal("expected expiration error")
	}
}

func TestSignedLOBTokenWithFingerprint(t *testing.T) {
	src := Source{ID: "src1", Kind: KindOracle, Username: "scott", Host: "127.0.0.1", Port: 1521}
	fp := SourceFingerprint(src)
	keys := map[string]any{"ID": 101}
	pks := []string{"ID"}

	token, err := GenerateSignedLOBTokenWithFingerprint(src.ID, fp, src.Username, "HR", "EMPLOYEES", "RESUME", "CLOB", "", keys, pks, "sess-1", 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateSignedLOBTokenWithFingerprint failed: %v", err)
	}

	payload, _, err := VerifySignedLOBToken(token)
	if err != nil {
		t.Fatalf("VerifySignedLOBToken failed: %v", err)
	}

	if payload.SourceFingerprint != fp {
		t.Fatalf("expected fingerprint %s, got %s", fp, payload.SourceFingerprint)
	}
}
