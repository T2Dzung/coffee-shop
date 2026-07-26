# Kubernetes source-of-truth layout

Thư mục này phân loại theo ownership, env

```text
apps/
└── coffeeshop/
    ├── base/          Portable workload bundle dùng chung cho DEV và PROD
    └── overlays/
        ├── dev/       DEV ECR tags và DEV-specific patches
        └── prod/      PROD ECR digest, config và ALB Ingress

environments/
├── dev/
│   ├── bootstrap/     DEV Argo CD root Application
│   ├── gitops/        DEV applications, add-ons và stateful charts
│   ├── network/       DEV Cilium/Gateway resources
│   └── policies/      DEV workload network policies
└── prod/
    ├── bootstrap/     Bounded PROD AppProject/Application
    └── platform/      PROD Argo CD và AWS Load Balancer Controller values
```

## Ownership rules

- Repository default branch là Git source of truth duy nhất cho cả hai environment.
  Git-backed Argo sources dùng `HEAD`; chart source vẫn pin version cụ thể.
- PR validation không publish release artifact. Sau merge, workflow build source SHA tối
  đa một lần, pin DEV bằng digest và chỉ PROD-promote candidate có QA evidence khớp.
- `apps/coffeeshop/base` không được chứa AWS account ID, registry của một
  môi trường hoặc bootstrap của cluster.
- Environment-owned image identity chỉ nằm trong `overlays/dev` hoặc `overlays/prod`.
- DEV root Application chỉ recurse `environments/dev/gitops/applications`.
- PROD bootstrap chỉ target `apps/coffeeshop/overlays/prod`; nó không recurse DEV.
- Tài nguyên phase cũ, probe tạm và manifest test không được giữ trong active tree.
  Evidence lịch sử nằm trong tài liệu phase và Git history.
- Layout contract được enforce bởi typed validation entrypoint
  `bash scripts/validate-infra.sh` (Kubernetes scope/profile tương ứng).
