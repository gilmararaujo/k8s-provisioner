package executor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileExists_True(t *testing.T) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "test_file_*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	exists := FileExists(tmpFile.Name())

	assert.True(t, exists, "FileExists should return true for existing file")
}

func TestFileExists_False(t *testing.T) {
	exists := FileExists("/nonexistent/path/to/file.txt")

	assert.False(t, exists, "FileExists should return false for nonexistent file")
}

func TestFileExists_Directory(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "test_dir_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	exists := FileExists(tmpDir)

	assert.True(t, exists, "FileExists should return true for directories too")
}

func TestWriteFile_Success(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "test_write_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test.txt")
	content := "Hello, World!"

	err = WriteFile(filePath, content)

	require.NoError(t, err, "WriteFile should not return error")

	// Verify content was written
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestWriteFile_Overwrite(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "test_write_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test.txt")

	// Write initial content
	err = WriteFile(filePath, "Initial content")
	require.NoError(t, err)

	// Overwrite with new content
	newContent := "New content"
	err = WriteFile(filePath, newContent)
	require.NoError(t, err)

	// Verify new content
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, newContent, string(data))
}

func TestWriteFile_InvalidPath(t *testing.T) {
	err := WriteFile("/nonexistent/directory/file.txt", "content")

	assert.Error(t, err, "WriteFile should return error for invalid path")
}

func TestAppendToFile_Success(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "test_append_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test.txt")

	// Write initial content
	err = WriteFile(filePath, "Line 1\n")
	require.NoError(t, err)

	// Append content
	err = AppendToFile(filePath, "Line 2\n")
	require.NoError(t, err)

	// Verify combined content
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "Line 1\nLine 2\n", string(data))
}

func TestAppendToFile_CreateNew(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "test_append_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "new_file.txt")
	content := "New content"

	// Append to nonexistent file (should create it)
	err = AppendToFile(filePath, content)
	require.NoError(t, err)

	// Verify content
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestAppendToFile_Multiple(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "test_append_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test.txt")

	// Multiple appends
	err = AppendToFile(filePath, "A")
	require.NoError(t, err)
	err = AppendToFile(filePath, "B")
	require.NoError(t, err)
	err = AppendToFile(filePath, "C")
	require.NoError(t, err)

	// Verify combined content
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "ABC", string(data))
}

func TestNew(t *testing.T) {
	exec := New(true)

	assert.NotNil(t, exec)
	assert.True(t, exec.Verbose)

	exec2 := New(false)
	assert.False(t, exec2.Verbose)
}
