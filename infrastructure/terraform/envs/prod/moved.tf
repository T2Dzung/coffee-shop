moved {
  from = module.eks_cluster.aws_eks_addon.coredns
  to   = aws_eks_addon.coredns
}
