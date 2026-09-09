package httpserver

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestReviewSessionUserIsolation(t *testing.T) {
	request := func(name string) string {
		r := httptest.NewRequest("POST", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), authUserKey, &authUser{Name: name}))
		return scopedDatabaseSessionID(r, "tab-1")
	}
	if request("alice") == request("bob") {
		t.Fatal("users share transaction identity")
	}
	if request("alice") != request("alice") || !validDatabaseSessionID(request("alice")) {
		t.Fatal("unstable or invalid session")
	}
	if scopedDatabaseSessionID(httptest.NewRequest("POST", "/", nil), "tab-1") != "tab-1" {
		t.Fatal("local mode changed")
	}
}

func TestReviewNegativeCSVExpression(t *testing.T) {
	if safeCSVCell("-2+3+cmd") != "'-2+3+cmd" {
		t.Fatal("negative expression not protected")
	}
	if safeCSVCell("-12.5") != "-12.5" {
		t.Fatal("negative number changed")
	}
}
