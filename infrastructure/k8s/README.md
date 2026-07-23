# Kubernetes source-of-truth layout

Thư mục này phân loại theo ownership, env
```text
apps/
└── coffeeshop/
    ├── base/          Full application resources dùng cho DEV
    ├── components/    Reusable bounded slices, không chứa environment identity
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

- `apps/coffeeshop/base` và `components` không được chứa AWS account ID, registry của một
  môi trường hoặc bootstrap của cluster.
- Environment-owned image identity chỉ nằm trong `overlays/dev` hoặc `overlays/prod`.
- DEV root Application chỉ recurse `environments/dev/gitops/applications`.
- PROD bootstrap chỉ target `apps/coffeeshop/overlays/prod`; nó không recurse DEV.
- Tài nguyên phase cũ, probe tạm và manifest test không được giữ trong active tree.
  Evidence lịch sử nằm trong tài liệu phase và Git history.
- Layout contract được enforce bởi
  `scripts/validation/validate-k8s-layout.sh`.
