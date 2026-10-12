package secret

// Box 负责把敏感字节（SMTP 授权码）加密后交给 config 落盘。
// Windows 实现使用 DPAPI（按当前用户绑定），开发平台实现为明文透传（仅限 dev）。
type Box interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}
