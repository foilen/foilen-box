package keypair

import "testing"

func TestGenerateThenImportRoundTrip(t *testing.T) {
	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if generated.ID == "" || generated.PrivateKeyBase64 == "" {
		t.Fatalf("Generate() returned incomplete keypair: %+v", generated)
	}

	imported, err := Import(generated.PrivateKeyBase64)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if imported.ID != generated.ID {
		t.Errorf("Import() ID = %q, want %q", imported.ID, generated.ID)
	}
	if imported.PrivateKeyBase64 != generated.PrivateKeyBase64 {
		t.Errorf("Import() PrivateKeyBase64 mismatch")
	}
}

func TestImportInvalidBase64(t *testing.T) {
	if _, err := Import("not-base64!!!"); err == nil {
		t.Error("Import() expected error for invalid base64, got nil")
	}
}

func TestImportInvalidKeyBytes(t *testing.T) {
	if _, err := Import("aGVsbG8="); err == nil {
		t.Error("Import() expected error for invalid key bytes, got nil")
	}
}

func TestGenerateProducesDistinctKeys(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if a.ID == b.ID {
		t.Error("Generate() produced identical peer IDs across two calls")
	}
}
