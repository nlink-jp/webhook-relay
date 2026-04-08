package middleware

import (
	"strings"
	"testing"
)

func TestValidatePath(t *testing.T) {
	validate := ValidatePath([]string{".eml", ".msg"})

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		// Valid paths
		{"valid eml", "inbox/alert.eml", false},
		{"valid msg", "inbox/report.msg", false},
		{"valid nested", "inbox/2026/04/test.eml", false},
		{"double dot in name", "inbox/file..eml", false},
		{"hyphen underscore", "inbox/my-file_v2.eml", false},
		{"unicode filename", "inbox/テスト.eml", false},

		// Empty / missing
		{"empty path", "", true},
		{"no extension", "inbox/file", true},

		// Directory traversal variants
		{"traversal dotdot prefix", "../etc/passwd", true},
		{"traversal dotdot mid", "inbox/../../secret.eml", true},
		{"traversal dot prefix", "./inbox/test.eml", true},
		{"traversal dotdot only", "..", true},
		{"traversal dot only", ".", true},
		{"traversal triple dot", "inbox/.../test.eml", true}, // path.Clean changes this

		// Path normalization attacks
		{"leading slash", "/inbox/test.eml", true},
		{"trailing slash", "inbox/test.eml/", true},
		{"double slash", "inbox//test.eml", true},

		// URL-encoded traversal (Go net/http decodes %2e to . before we see it,
		// but if raw %XX somehow survives, reject it)
		{"percent in path", "inbox/%2e%2e/test.eml", true}, // path.Clean normalizes

		// Extension filter
		{"disallowed extension", "inbox/file.txt", true},
		{"disallowed exe", "inbox/malware.exe", true},
		{"double extension", "inbox/file.txt.eml", false}, // ext is .eml, valid
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

func TestValidatePathNullByte(t *testing.T) {
	validate := ValidatePath([]string{".eml"})
	if err := validate("inbox/test.eml\x00.txt"); err == nil {
		t.Error("expected error for null byte in path")
	}
}

func TestValidatePathTooLong(t *testing.T) {
	validate := ValidatePath([]string{".eml"})
	longDir := strings.Repeat("a", 895)
	longPath := longDir + "/test.eml"
	if err := validate(longPath); err == nil {
		t.Error("expected error for path exceeding 900 bytes")
	}
}

func TestValidatePathControlChars(t *testing.T) {
	validate := ValidatePath([]string{".eml"})
	tests := []struct {
		name string
		path string
	}{
		{"tab", "inbox/te\tst.eml"},
		{"newline", "inbox/te\nst.eml"},
		{"carriage return", "inbox/te\rst.eml"},
		{"bell", "inbox/te\x07st.eml"},
		{"backslash", "inbox\\test.eml"},
		{"delete char", "inbox/te\x7fst.eml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validate(tt.path); err == nil {
				t.Errorf("expected error for control char in path %q", tt.path)
			}
		})
	}
}
