package middleware

import (
	"testing"
)

func TestValidatePath(t *testing.T) {
	validate := ValidatePath([]string{".eml", ".msg"})

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid eml", "inbox/alert.eml", false},
		{"valid msg", "inbox/report.msg", false},
		{"valid nested", "inbox/2026/04/test.eml", false},
		{"empty path", "", true},
		{"traversal dotdot", "../etc/passwd", true},
		{"traversal mid", "inbox/../../secret.eml", true},
		{"disallowed extension", "inbox/file.txt", true},
		{"no extension", "inbox/file", true},
		{"double dot in name", "inbox/file..eml", false},
		{"traversal dot", "./inbox/test.eml", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePathNoExtensionFilter(t *testing.T) {
	validate := ValidatePath(nil)
	if err := validate("inbox/anything.xyz"); err != nil {
		t.Errorf("expected no error with nil extensions, got: %v", err)
	}
}
