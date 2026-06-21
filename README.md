# K8s MyApp Operator

一个用 Go + Kubebuilder 编写的 Kubernetes Operator，定义了一个自定义资源 `MyApp`，并实现控制循环（Reconcile Loop），自动根据 `MyApp` 的定义创建、更新对应的 Deployment。

## 功能

- 定义自定义资源 `MyApp`（CRD），用户只需声明期望的副本数
- 控制器持续监听 `MyApp` 的创建/更新事件
- 自动创建对应的 Deployment，并使其副本数与 `MyApp.spec.replicas` 保持一致
- 通过 owner reference 建立 MyApp 与 Deployment 的所属关系：删除 MyApp 会自动级联删除对应 Deployment
- 修改 `MyApp.spec.replicas` 后，控制器会自动调谐 Deployment 副本数，使其趋同于期望状态

## 技术栈

- Go
- [Kubebuilder](https://book.kubebuilder.io/)：用于生成 Operator 项目骨架、CRD 定义
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)：实现调谐循环的核心库
- client-go

## 使用方式

### 1. 安装 CRD 到集群

\`\`\`bash
make install
\`\`\`

### 2. 运行控制器（本地调试模式）

\`\`\`bash
make run
\`\`\`

### 3. 创建一个 MyApp 资源

\`\`\`yaml
apiVersion: app.demo.io/v1
kind: MyApp
metadata:
  name: my-app-example
spec:
  replicas: 3
\`\`\`

\`\`\`bash
kubectl apply -f myapp-sample.yaml
\`\`\`

控制器会自动创建一个同名的 Deployment，副本数为3。

### 4. 验证

\`\`\`bash
kubectl get deployments
kubectl get pods -l app=my-app-example
\`\`\`

### 5. 修改副本数，验证自动调谐

\`\`\`bash
kubectl patch myapp my-app-example --type=merge -p '{"spec":{"replicas":5}}'
kubectl get deployments
\`\`\`

Deployment 的副本数会自动同步为5，无需手动操作。

## 开发与测试环境

本项目在 WSL2 + K3s 单节点集群上开发和验证。
