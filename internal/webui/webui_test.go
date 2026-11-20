package webui

import "testing"

func TestNotARoute(t *testing.T) {
	for p, want := range map[string]bool{
		"p/SHOP": false, "settings/security": false, "tasks/SHOP-12": false, "wp-content/backups": false,
		"wp-login.php": true, ".env": true, ".git/config": true, "var/log/apache2/error.log": true, "backup.sql": true,
	} {
		if got := notARoute(p); got != want {
			t.Errorf("notARoute(%q) = %v, want %v", p, got, want)
		}
	}
}
