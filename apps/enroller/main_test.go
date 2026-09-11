package main

import (
	"strings"
	"testing"
)

func TestRenderServiceUnitBindsServerURL(t *testing.T) {
	rendered, err := renderServiceUnit([]byte("Environment=\"SCOUT_SERVER_URL=__SCOUT_SERVER_URL__\"\n"), "https://scout.example.test:8443")
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "Environment=\"SCOUT_SERVER_URL=https://scout.example.test:8443\"\n" {
		t.Fatalf("unexpected rendered service unit: %s", rendered)
	}
	if strings.Contains(string(rendered), "__SCOUT_SERVER_URL__") {
		t.Fatal("service URL placeholder was not removed")
	}
}

func TestRenderServiceUnitRejectsUnsafeOrUnmarkedInput(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		template []byte
		server   string
	}{
		{name: "unsafe url", template: []byte("__SCOUT_SERVER_URL__"), server: "https://scout.example.test\nExecStart=/bin/sh"},
		{name: "missing marker", template: []byte("Environment=SCOUT_SERVER_URL=old"), server: "https://scout.example.test"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if _, err := renderServiceUnit(fixture.template, fixture.server); err == nil {
				t.Fatal("unsafe or unmarked service unit was accepted")
			}
		})
	}
}
