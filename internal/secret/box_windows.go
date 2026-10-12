package secret

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	crypt32                = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

const cryptProtectUiForbidden = 0x1

// DPAPIBox 使用 Windows DPAPI（当前用户范围）加密，密文仅能在同用户下解开。
type DPAPIBox struct{}

func NewDPAPIBox() *DPAPIBox { return &DPAPIBox{} }

var dpapiEntropy = []byte("StorageCheck.v1.entropy")

func (b *DPAPIBox) Seal(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, nil
	}
	in := dataBlob{cbData: uint32(len(plain)), pbData: &plain[0]}
	ent := dataBlob{cbData: uint32(len(dpapiEntropy)), pbData: &dpapiEntropy[0]}
	var out dataBlob
	ret, errno, _ := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&ent)), 0, 0,
		cryptProtectUiForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, dpapiErr("加密", errno)
	}
	return takeBlob(out), nil
}

func (b *DPAPIBox) Open(sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	in := dataBlob{cbData: uint32(len(sealed)), pbData: &sealed[0]}
	ent := dataBlob{cbData: uint32(len(dpapiEntropy)), pbData: &dpapiEntropy[0]}
	var out dataBlob
	ret, errno, _ := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&ent)), 0, 0,
		cryptProtectUiForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, dpapiErr("解密", errno)
	}
	return takeBlob(out), nil
}

// takeBlob 把 DPAPI 分配的缓冲区复制进 Go 内存后再释放。
// 直接返回 unsafe.Slice 会在 LocalFree 之后读到已释放（并可能被下次 DPAPI 调用复用）的内存。
func takeBlob(out dataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.pbData)))
	if out.cbData == 0 {
		return nil
	}
	src := unsafe.Slice(out.pbData, out.cbData)
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// dpapiErr DPAPI 失败时常常不设置 last error（返回 0），给出可操作的中文提示。
func dpapiErr(op string, errno uintptr) error {
	if errno == 0 {
		return fmt.Errorf("dpapi: 授权码%s失败（密文损坏或不属于当前 Windows 用户，请重新填写授权码）", op)
	}
	return fmt.Errorf("dpapi: 授权码%s失败: %w", op, syscall.Errno(errno))
}
