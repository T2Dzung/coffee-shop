# Phase 6.0 Toolchain Matrix

Ngày xác minh: 2026-07-10  
Target API compatibility: Kubernetes/K3s 1.35

| Component | Version | SHA-256 / source of truth |
|---|---:|---|
| Go | 1.26.2 | `/usr/local/go/bin/go version`; `go.mod` |
| Kubebuilder | 4.14.0 | `a433730088e4890b138c06e2362163cde459622b72eeccf61955f22de86fca86` |
| controller-tools | 0.20.1 | `295e3e31749104cc792952c59acb38616584c28f90603e18e9f383f9ae2f4f4b` |
| controller-runtime | 0.23.3 | `go.mod`/`go.sum` |
| Kubernetes Go libraries | 0.35.0 | `go.mod`/`go.sum` |
| golangci-lint | 2.11.4 | `0b01bcccd2593c9e41fa9c22002f6488c4be58ec0fdb54c52e3c9c94164f81b8` |
| setup-envtest | release-0.23 | `85c75a4b8993903615b033ce4b2b5f37666fe666912fa4448d69eadbad0fe7f1` |
| envtest kube-apiserver | 1.35.0 | `999e4874de50139f40929d6a9f4844efc66ad09647dc0e84031daca4711f12b6` |
| envtest etcd | 1.35.0 | `e3d7d2950b4efbebb7c585a5876c2dc7db4a0c4b8ed95939121be853e0c55a27` |
| envtest kubectl | 1.35.0 | `a2e984a18a0c063279d692533031c1eff93a262afcc0afdc517375432d060989` |
| kubeconform | 0.8.0 | Local validation dependency |

## Reproducibility rules

- Tool versions trong Makefile không dùng `latest`.
- `LOCALBIN=bin` tránh Make parse lỗi khi workspace path chứa dấu cách.
- `GOBIN=$(abspath $(LOCALBIN))` đáp ứng yêu cầu absolute path của `go install`.
- Symlink check dùng `readlink -f` để `make manifests generate` không reinstall tool mỗi lần.
- Generated CRD/RBAC/deepcopy chỉ thay đổi qua `make manifests generate`.
- Envtest asset được pin Kubernetes 1.35 để khớp K3s target, không tự chọn latest.
