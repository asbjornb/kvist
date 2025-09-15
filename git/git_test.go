package git

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsBinaryFile(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "kvist_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name       string
		content    []byte
		expectBinary bool
		description string
	}{
		{
			name:       "text_file.go",
			content:    []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n"),
			expectBinary: false,
			description: "Simple Go source file",
		},
		{
			name:       "text_with_crlf.go",
			content:    []byte("package main\r\n\r\nimport \"fmt\"\r\n\r\nfunc main() {\r\n\tfmt.Println(\"Hello, World!\")\r\n}\r\n"),
			expectBinary: false,
			description: "Go source file with CRLF line endings",
		},
		{
			name:       "binary_file.bin",
			content:    []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 0x00, 0x00, 0x48, 0x65, 0x6C, 0x6C, 0x6F},
			expectBinary: true,
			description: "Binary file with null bytes",
		},
		{
			name:       "mostly_binary.dat",
			content:    createMostlyBinaryContent(),
			expectBinary: true,
			description: "File with >30% non-printable characters",
		},
		{
			name:       "empty_file.txt",
			content:    []byte{},
			expectBinary: false,
			description: "Empty file",
		},
		{
			name:       "unicode_text.txt",
			content:    []byte("Hello 世界! 🌍\nThis is UTF-8 text with unicode characters.\n"),
			expectBinary: false,
			description: "UTF-8 text with unicode characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			filePath := filepath.Join(tempDir, tt.name)
			err := os.WriteFile(filePath, tt.content, 0644)
			if err != nil {
				t.Fatalf("Failed to create test file %s: %v", tt.name, err)
			}

			// Test binary detection
			result := IsBinaryFile(tempDir, tt.name)
			if result != tt.expectBinary {
				t.Errorf("IsBinaryFile(%s) = %v, expected %v (%s)",
					tt.name, result, tt.expectBinary, tt.description)

				// Additional debugging info
				if len(tt.content) > 0 && len(tt.content) <= 100 {
					t.Logf("File content (first %d bytes): %q", len(tt.content), tt.content)
					t.Logf("Content as hex: % x", tt.content)
				} else if len(tt.content) > 100 {
					t.Logf("File content (first 100 bytes): %q", tt.content[:100])
					t.Logf("Content as hex: % x", tt.content[:100])
				}

				// Analyze the content like our function does
				debugBinaryDetection(t, tt.content, tt.name)
			}
		})
	}
}

// Test with actual main.go file
func TestMainGoIsNotBinary(t *testing.T) {
	// Test with the actual main.go in the parent directory
	result := IsBinaryFile("..", "main.go")
	if result {
		t.Errorf("main.go should not be detected as binary")

		// Debug the actual main.go
		content, err := os.ReadFile("../main.go")
		if err != nil {
			t.Logf("Could not read main.go for debugging: %v", err)
			return
		}

		if len(content) > 512 {
			content = content[:512]
		}

		debugBinaryDetection(t, content, "main.go")
	}
}

func createMostlyBinaryContent() []byte {
	content := make([]byte, 100)
	// Fill with mostly non-printable characters (>30%)
	for i := 0; i < 40; i++ {
		content[i] = byte(i % 8) // Non-printable control characters
	}
	// Add some printable content
	copy(content[40:], []byte("Some readable text here to make it mixed content"))
	return content
}

func debugBinaryDetection(t *testing.T, content []byte, filename string) {
	if len(content) == 0 {
		t.Logf("Debug %s: Empty file", filename)
		return
	}

	n := len(content)
	if n > 512 {
		n = 512
	}

	// Check for null bytes
	nullBytes := 0
	for i := 0; i < n; i++ {
		if content[i] == 0 {
			nullBytes++
		}
	}
	t.Logf("Debug %s: Null bytes found: %d", filename, nullBytes)

	if nullBytes > 0 {
		t.Logf("Debug %s: File marked as binary due to null bytes", filename)
		return
	}

	// Check non-printable characters
	nonPrintable := 0
	nonPrintableChars := []string{}
	for i := 0; i < n; i++ {
		if content[i] < 9 || (content[i] > 13 && content[i] < 32) || content[i] > 126 {
			nonPrintable++
			if len(nonPrintableChars) < 10 { // Show first 10
				nonPrintableChars = append(nonPrintableChars,
					fmt.Sprintf("pos:%d val:%d(0x%02x)", i, content[i], content[i]))
			}
		}
	}

	percentage := float64(nonPrintable) / float64(n) * 100
	t.Logf("Debug %s: Non-printable chars: %d/%d (%.2f%%)", filename, nonPrintable, n, percentage)
	t.Logf("Debug %s: Threshold: >30%%, Result: binary=%v", filename, percentage > 30)

	if len(nonPrintableChars) > 0 {
		t.Logf("Debug %s: First non-printable chars: %v", filename, nonPrintableChars)
	}
}

func TestDiffNumstat(t *testing.T) {
	// This test requires being in a git repository with actual changes
	// We'll test the basic functionality
	stats, err := DiffNumstat(".", false) // unstaged changes
	if err != nil {
		t.Logf("DiffNumstat failed (this may be expected if no git repo): %v", err)
		return
	}

	t.Logf("Found %d unstaged changes", len(stats))
	for i, stat := range stats {
		if i < 3 { // Only log first few to avoid spam
			isBinary := stat.Added == "-" && stat.Deleted == "-"
			t.Logf("  %s: +%s -%s (binary: %v)", stat.Path, stat.Added, stat.Deleted, isBinary)
		}
	}
}

func TestIsBinaryChange(t *testing.T) {
	// Test with a known text file (this test file itself)
	isBinary, err := IsBinaryChange("..", false, "git/git_test.go")
	if err != nil {
		t.Logf("IsBinaryChange failed (this may be expected if no changes): %v", err)
		return
	}

	if isBinary {
		t.Errorf("git_test.go should not be detected as binary change")
	}

	t.Logf("git_test.go binary change: %v", isBinary)
}