package converter

import (
	"strings"
	"testing"
)

func TestValidate7zListingAcceptsSafeArchive(t *testing.T) {
	listing := []byte(`
Path = archive.zip
Type = zip

----------
Path = docs/readme.txt
Folder = -
Size = 12

Path = images
Folder = +
Size = 0
`)
	if err := validate7zListing(listing, 10, 1024); err != nil {
		t.Fatalf("expected safe archive listing: %v", err)
	}
}

func TestValidate7zListingAccepts7zDirectoryWithoutFolderField(t *testing.T) {
	listing := []byte(`
Path = archive.7z
Type = 7z

----------
Path = dir
Size = 0
Packed Size = 0
Attributes = D drwxr-xr-x

Path = dir/file.txt
Size = 1
Packed Size = 5
Attributes = A -rw-r--r--
`)
	if err := validate7zListing(listing, 1, 1024); err != nil {
		t.Fatalf("expected 7z directory to be ignored for file count: %v", err)
	}
}

func TestValidate7zListingRejectsTraversal(t *testing.T) {
	listing := []byte(`
----------
Path = ../outside.txt
Folder = -
Size = 1
`)
	err := validate7zListing(listing, 10, 1024)
	if err == nil || !strings.Contains(err.Error(), "escapes output directory") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestValidate7zListingRejectsNestedTraversalSegment(t *testing.T) {
	listing := []byte(`
----------
Path = safe/../outside.txt
Folder = -
Size = 1
`)
	err := validate7zListing(listing, 10, 1024)
	if err == nil || !strings.Contains(err.Error(), "escapes output directory") {
		t.Fatalf("expected nested traversal rejection, got %v", err)
	}
}

func TestValidate7zListingRejectsBackslashPath(t *testing.T) {
	listing := []byte(`
----------
Path = dir\evil.txt
Folder = -
Size = 1
`)
	err := validate7zListing(listing, 10, 1024)
	if err == nil || !strings.Contains(err.Error(), "unsafe path separator") {
		t.Fatalf("expected unsafe separator rejection, got %v", err)
	}
}

func TestValidate7zListingAcceptsBenignDotAndColonPaths(t *testing.T) {
	listing := []byte(`
----------
Path = .
Folder = +
Size = 0

Path = ./docs/readme.txt
Folder = -
Size = 1

Path = photo:edited.jpg
Folder = -
Size = 1
`)
	if err := validate7zListing(listing, 10, 1024); err != nil {
		t.Fatalf("expected benign dot and colon paths to be accepted: %v", err)
	}
}

func TestValidate7zListingRejectsWindowsDrivePath(t *testing.T) {
	listing := []byte(`
----------
Path = C:/Users/example/file.txt
Folder = -
Size = 1
`)
	err := validate7zListing(listing, 10, 1024)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected Windows drive path rejection, got %v", err)
	}
}

func TestValidate7zListingRejectsTooLarge(t *testing.T) {
	listing := []byte(`
----------
Path = big.bin
Folder = -
Size = 2048
`)
	err := validate7zListing(listing, 10, 1024)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}
