package control

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDB struct{ err error }

func (db fakeDB) PingContext(context.Context) error { return db.err }

func TestStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   Database
		want string
	}{
		{"missing", nil, "not configured"},
		{"ready", fakeDB{}, "connected"},
		{"failure", fakeDB{errors.New("secret connection string")}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			Handler(tc.db).ServeHTTP(response, httptest.NewRequest("GET", "/api/status", nil))
			body := response.Body.String()
			if response.Code != 200 || !strings.Contains(body, tc.want) {
				t.Fatalf("unexpected response: %d %s", response.Code, body)
			}
			if strings.Contains(body, "secret") {
				t.Fatal("database error leaked")
			}
		})
	}
}
