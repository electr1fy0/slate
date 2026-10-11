package config

import "testing"

func TestEmailConfigurationRequiresCompleteDeliverySettings(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantError bool
	}{
		{name: "email disabled", env: map[string]string{}},
		{name: "complete unauthenticated sender", env: map[string]string{"SMTP_HOST": "localhost", "SMTP_FROM": "teacher@example.com", "PUBLIC_BASE_URL": "http://localhost:3000/"}},
		{name: "complete authenticated sender", env: map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "teacher@example.com", "SMTP_USERNAME": "teacher", "SMTP_PASSWORD": "test-password", "SMTP_PORT": "2525", "PUBLIC_BASE_URL": "https://school.example.com/"}},
		{name: "host without sender", env: map[string]string{"SMTP_HOST": "smtp.example.com"}, wantError: true},
		{name: "sender without host", env: map[string]string{"SMTP_FROM": "teacher@example.com"}, wantError: true},
		{name: "missing link origin", env: map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "teacher@example.com"}, wantError: true},
		{name: "missing password", env: map[string]string{"SMTP_USERNAME": "teacher"}, wantError: true},
		{name: "missing username", env: map[string]string{"SMTP_PASSWORD": "test-password"}, wantError: true},
		{name: "invalid port", env: map[string]string{"SMTP_PORT": "not-a-port"}, wantError: true},
		{name: "out of range port", env: map[string]string{"SMTP_PORT": "65536"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, err := LoadFrom(func(name string) string { return tt.env[name] })
			if (err != nil) != tt.wantError {
				t.Fatalf("LoadFrom error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && tt.env["SMTP_HOST"] != "" {
				if settings.SMTPHost != tt.env["SMTP_HOST"] || settings.SMTPFrom != tt.env["SMTP_FROM"] || settings.PublicBaseURL == "" || settings.PublicBaseURL[len(settings.PublicBaseURL)-1] == '/' {
					t.Fatalf("incomplete or unnormalized sender settings: %#v", settings)
				}
			}
		})
	}
}
