package auth

import "testing"

func TestIssueAndParse(t *testing.T) {
	token, err := Issue("test-secret", "user-1", "admin", "admin", 3)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Parse("test-secret", token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "user-1" || claims.Username != "admin" || claims.Role != "admin" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if _, err := Parse("wrong-secret", token); err == nil {
		t.Fatal("expected signature validation to fail")
	}
}
