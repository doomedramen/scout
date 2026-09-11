package auth

import "testing"

func TestHashPasswordAllowsEightCharacterComplexPassword(t *testing.T) {
	hash, err := HashPassword("Aa123456")
	if err != nil {
		t.Fatalf("eight-character password was rejected: %v", err)
	}
	if !CheckPassword(hash, "Aa123456") {
		t.Fatal("hashed eight-character password did not verify")
	}
}

func TestHashPasswordRejectsShortOrWeakPasswords(t *testing.T) {
	for _, password := range []string{"Aa12345", "aa123456", "AA123456", "Aaabcdef"} {
		if _, err := HashPassword(password); err == nil {
			t.Fatalf("weak password %q was accepted", password)
		}
	}
}
