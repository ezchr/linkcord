package auth

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect   = crypt32.NewProc("CryptUnprotectData")
	procLocalFree        = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

type dataBlob struct {
	n    uint32
	data *byte
}

func blobOf(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{n: uint32(len(b)), data: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	out := make([]byte, b.n)
	copy(out, unsafe.Slice(b.data, b.n))
	return out
}

// protect encrypts b for the Windows user signed in (DPAPI): only that user,
// on this computer, can decrypt it.
func protect(b []byte) ([]byte, error) {
	var out dataBlob
	// CRYPTPROTECT_UI_FORBIDDEN
	r, _, err := procCryptProtectData.Call(uintptr(unsafe.Pointer(blobOf(b))), 0, 0, 0, 0, 0x1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}

// unprotect decrypts what protect encrypted.
func unprotect(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty")
	}
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(uintptr(unsafe.Pointer(blobOf(b))), 0, 0, 0, 0, 0x1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}
