package auth

import "testing"

func TestLoginCreatesSession(t *testing.T) {
	service := NewService()

	result, err := service.Login("DonVito")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	if result.User.ID == "" {
		t.Fatal("expected user id")
	}
	if result.User.Nickname != "DonVito" {
		t.Fatalf("expected nickname DonVito, got %q", result.User.Nickname)
	}
	if result.Token == "" {
		t.Fatal("expected token")
	}

	user, ok := service.UserByToken(result.Token)
	if !ok {
		t.Fatal("expected token to resolve user")
	}
	if user.ID != result.User.ID {
		t.Fatalf("expected user id %q, got %q", result.User.ID, user.ID)
	}
}

func TestLoginRejectsInvalidNickname(t *testing.T) {
	service := NewService()

	if _, err := service.Login("x"); err != ErrInvalidNickname {
		t.Fatalf("expected ErrInvalidNickname, got %v", err)
	}
}

func TestLogoutRemovesSession(t *testing.T) {
	service := NewService()
	result, err := service.Login("Medic")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	service.Logout(result.Token)

	if _, ok := service.UserByToken(result.Token); ok {
		t.Fatal("expected logout to remove session")
	}
}
