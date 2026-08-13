\# Kubernetes MyApp Operator



一个基于 Go、Kubebuilder 与 controller-runtime 开发的 Kubernetes Operator。



项目通过自定义资源 `MyApp` 描述应用期望状态，由 Controller 持续执行 Reconcile，将声明式配置转换为 Deployment、Service、ConfigMap 与 HPA，并提供状态管理、事件记录、漂移检测、自愈、监控指标、错误分类与 Helm 交付能力。



项目重点不是简单创建 Kubernetes 资源，而是实现一个完整的声明式控制闭环：



&#x20;   MyApp Spec

&#x20;       ↓

&#x20;   Reconcile Loop

&#x20;       ↓

&#x20;   Desired State

&#x20;       ↓

&#x20;   Deployment / Service / ConfigMap / HPA

&#x20;       ↓

&#x20;   Drift Detection

&#x20;       ↓

&#x20;   Self-Healing

&#x20;       ↓

&#x20;   Status / Conditions / Events / Metrics



\---



\## 项目能力



\### 1. MyApp CRD



定义 `app.demo.io/v1` 自定义资源 `MyApp`。



当前支持声明：



\- 镜像

\- 副本数

\- 服务端口

\- 环境配置

\- HPA 自动扩缩容参数



示例：



&#x20;   apiVersion: app.demo.io/v1

&#x20;   kind: MyApp

&#x20;   metadata:

&#x20;     name: helm-demo

&#x20;     namespace: myapp-demo

&#x20;   spec:

&#x20;     replicas: 2

&#x20;     image: nginx:1.27-alpine

&#x20;     port: 80

&#x20;     config:

&#x20;       APP\_ENV: helm-validation

&#x20;       DELIVERY\_MODE: helm

&#x20;     autoscaling:

&#x20;       enabled: true

&#x20;       minReplicas: 1

&#x20;       maxReplicas: 3

&#x20;       targetCPUUtilizationPercentage: 60



\---



\### 2. Deployment 管理



Controller 根据 `MyApp.spec` 自动创建并持续维护 Deployment。



支持：



\- 首次创建 Deployment

\- 镜像同步

\- 副本同步

\- Container Port 同步

\- Labels 同步

\- RollingUpdate

\- OwnerReference

\- Deployment 删除后自动重建

\- 多次 Reconcile 幂等

\- Spec 更新后自动同步

\- Deployment 漂移检测与自愈



当外部用户直接修改 Deployment，例如：



&#x20;   nginx:1.27-alpine

&#x20;   ↓

&#x20;   busybox:latest



Controller 会发现实际状态与 MyApp 声明不一致，并自动恢复期望镜像。



同时产生：



&#x20;   DeploymentDriftCorrected



Kubernetes Event。



\---



\### 3. Service 管理



Controller 自动创建：



&#x20;   <myapp-name>-service



并持续维护：



\- Selector

\- Service Port

\- TargetPort

\- Protocol

\- Labels

\- OwnerReference



如果外部直接修改 Service，例如破坏 Selector：



&#x20;   app=helm-demo

&#x20;   ↓

&#x20;   app=broken-demo



Controller 会自动恢复。



对应 Event：



&#x20;   ServiceDriftCorrected



\---



\### 4. ConfigMap 管理



`MyApp.spec.config` 可以声明应用环境配置。



例如：



&#x20;   config:

&#x20;     APP\_ENV: production

&#x20;     LOG\_LEVEL: info



Controller 自动生成：



&#x20;   <myapp-name>-config



支持：



\- 创建 ConfigMap

\- Data 同步

\- Labels 同步

\- OwnerReference

\- 漂移检测

\- 自动自愈

\- config 清空后自动删除 ConfigMap



对应漂移事件：



&#x20;   ConfigMapDriftCorrected



\---



\### 5. HPA 自动扩缩容



MyApp 支持声明 HPA：



&#x20;   autoscaling:

&#x20;     enabled: true

&#x20;     minReplicas: 1

&#x20;     maxReplicas: 3

&#x20;     targetCPUUtilizationPercentage: 60



Controller 自动创建：



&#x20;   <myapp-name>-hpa



支持：



\- minReplicas

\- maxReplicas

\- CPU utilization target

\- HPA 漂移检测

\- HPA 自动修复

\- autoscaling 关闭后删除 HPA



对应 Event：



&#x20;   HPADriftCorrected



\---



\## HPA Replica Ownership



当 HPA 未启用时：



&#x20;   MyApp.spec.replicas

&#x20;       ↓

&#x20;   Operator

&#x20;       ↓

&#x20;   Deployment.spec.replicas



Operator 负责维护副本数量。



当 HPA 启用后：



&#x20;   HPA

&#x20;    ↓

&#x20;   Deployment.spec.replicas



此时 Operator 不再持续强制修改 Deployment replicas。



这样可以避免：



&#x20;   Operator → replicas=2

&#x20;   HPA      → replicas=1

&#x20;   Operator → replicas=2

&#x20;   HPA      → replicas=1

&#x20;   ...



产生持续控制冲突。



K3s 实机验证中：



&#x20;   MyApp.spec.replicas = 2

&#x20;   HPA minReplicas = 1

&#x20;   HPA maxReplicas = 3

&#x20;   CPU = 0% / 60%



HPA 成功将 Deployment 从 2 个副本缩容至 1 个副本，Operator 没有重新覆盖 HPA 的结果。



\---



\## Status 与 Conditions



MyApp Status 用于描述 Controller 已观察到的实际状态。



当前状态字段包括：



\- `phase`

\- `readyReplicas`

\- `observedGeneration`

\- `conditions`



Conditions 包括：



\### Ready



表示应用是否已经达到可用状态。



示例：



&#x20;   type: Ready

&#x20;   status: "True"

&#x20;   reason: AllReplicasReady



\### Progressing



表示应用是否仍在创建或更新。



示例：



&#x20;   type: Progressing

&#x20;   status: "False"

&#x20;   reason: ReconcileComplete



\### Degraded



表示应用或受管资源是否处于异常状态。



示例：



&#x20;   type: Degraded

&#x20;   status: "False"

&#x20;   reason: ResourcesHealthy



`observedGeneration` 用于记录 Controller 当前已经处理到的 MyApp generation，避免使用旧状态描述新 Spec。



\---



\## Kubernetes Events



Controller 会为关键生命周期操作产生 Kubernetes Event。



资源创建：



&#x20;   CreatedDeployment

&#x20;   CreatedService

&#x20;   CreatedConfigMap

&#x20;   CreatedHPA



漂移修复：



&#x20;   DeploymentDriftCorrected

&#x20;   ServiceDriftCorrected

&#x20;   ConfigMapDriftCorrected

&#x20;   HPADriftCorrected



状态：



&#x20;   Ready



重试策略：



&#x20;   RetryScheduled

&#x20;   RetrySuppressed



可以通过：



&#x20;   kubectl get events \\

&#x20;     --field-selector involvedObject.kind=MyApp



查看。



\---



\## Finalizer 与生命周期管理



Controller 使用 Finalizer 管理 MyApp 删除生命周期。



同时通过 Controller OwnerReference 建立：



&#x20;   MyApp

&#x20;     ├── Deployment

&#x20;     ├── Service

&#x20;     ├── ConfigMap

&#x20;     └── HPA



的所有权关系。



删除 MyApp 后，由 Kubernetes Garbage Collector 负责级联删除其受管资源。



\---



\## 漂移检测与自愈



Operator 不仅负责资源首次创建，还持续监听受管资源。



当前支持：



| Resource | Drift Detection |

|---|---|

| Deployment | image、labels、strategy 等 |

| Service | selector、ports、protocol、labels |

| ConfigMap | data、labels |

| HPA | minReplicas、maxReplicas、CPU target 等 |



实机测试中同时人为制造：



&#x20;   Deployment image → busybox:latest

&#x20;   Service selector → broken-demo

&#x20;   ConfigMap data → manual-drift

&#x20;   HPA maxReplicas → 99



Controller 最终自动恢复为：



&#x20;   Deployment image = nginx:1.27-alpine

&#x20;   Service selector = app=helm-demo

&#x20;   APP\_ENV = helm-validation

&#x20;   DELIVERY\_MODE = helm

&#x20;   HPA minReplicas = 1

&#x20;   HPA maxReplicas = 3

&#x20;   CPU target = 60



并产生对应 DriftCorrected Events。



\---



\## Reconcile 错误分类与重试



Controller 对 Reconcile 错误进行分类，而不是所有错误都直接无限重试。



\### Transient Error



例如：



\- Conflict

\- TooManyRequests

\- Timeout

\- ServerTimeout

\- ServiceUnavailable



采用延迟重试。



示例：



&#x20;   Conflict → 2s

&#x20;   TooManyRequests → 15s

&#x20;   Timeout / ServerTimeout / ServiceUnavailable → 10s



\### Permanent Error



例如：



\- Unauthorized

\- Forbidden

\- Invalid

\- BadRequest



不进行主动快速重试。



\### Unknown Error



未识别错误使用延迟重试，避免 Controller 高频空转。



对应 Event：



&#x20;   RetryScheduled

&#x20;   RetrySuppressed



\---



\## Optimistic Concurrency



Kubernetes Resource 使用 `resourceVersion` 实现乐观并发控制。



在 HPA 与 Controller 同时更新资源的实机测试中观察到：



&#x20;   Operation cannot be fulfilled:

&#x20;   the object has been modified



Controller 随后的 Reconcile 会重新读取最新 ResourceVersion，并最终继续收敛到正确状态。



因此单次 Conflict 不会破坏最终一致性。



\---



\## Prometheus Metrics



Controller 暴露运行指标，用于观察 Reconcile 行为。



当前包括：



&#x20;   reconcile\_total

&#x20;   errors\_total

&#x20;   reconcile\_duration\_seconds



Helm 安装默认暴露 Metrics Service：



&#x20;   myapp-operator-metrics:8080



同时 Controller 提供：



&#x20;   /healthz

&#x20;   /readyz



健康检查端口：



&#x20;   8081



\---



\# Helm 安装



项目提供 Helm Chart：



&#x20;   charts/myapp-operator



Chart 当前包含：



\- CRD

\- ServiceAccount

\- ClusterRole

\- ClusterRoleBinding

\- Leader Election Role

\- Leader Election RoleBinding

\- Controller Deployment

\- Metrics Service



\---



\## Helm Lint



&#x20;   helm lint charts/myapp-operator



当前验证结果：



&#x20;   1 chart(s) linted, 0 chart(s) failed



\---



\## 安装 Operator



构建 Controller Image：



&#x20;   docker build \\

&#x20;     -t k8s-myapp-operator:dev \\

&#x20;     .



在本地 K3s 环境中，可导入 containerd：



&#x20;   docker save \\

&#x20;     k8s-myapp-operator:dev \\

&#x20;     -o /tmp/k8s-myapp-operator-dev.tar



&#x20;   sudo k3s ctr images import \\

&#x20;     /tmp/k8s-myapp-operator-dev.tar



Helm 安装：



&#x20;   helm upgrade --install myapp-operator \\

&#x20;     charts/myapp-operator \\

&#x20;     --namespace myapp-operator-system \\

&#x20;     --create-namespace \\

&#x20;     --set image.repository=k8s-myapp-operator \\

&#x20;     --set image.tag=dev \\

&#x20;     --set image.pullPolicy=IfNotPresent



检查：



&#x20;   helm list -n myapp-operator-system



&#x20;   kubectl get pods \\

&#x20;     -n myapp-operator-system



验证环境中 Controller 达到：



&#x20;   READY   STATUS

&#x20;   1/1     Running



\---



\## CRD 升级说明



Helm `crds/` 主要负责 CRD 首次安装。



如果集群已经存在历史版本 CRD，而项目更新了 CRD Schema，应显式更新：



&#x20;   kubectl apply \\

&#x20;     -f config/crd/bases/app.demo.io\_myapps.yaml



本项目实机升级过程中曾通过该方式更新：



&#x20;   status.observedGeneration



Schema。



\---



\# 创建 MyApp



创建 namespace：



&#x20;   kubectl create namespace myapp-demo



应用示例：



&#x20;   kubectl apply \\

&#x20;     -f config/samples/app\_v1\_myapp\_helm\_demo.yaml



查看 MyApp：



&#x20;   kubectl get myapp \\

&#x20;     -n myapp-demo \\

&#x20;     -o wide



查看受管资源：



&#x20;   kubectl get deployment,service,configmap,hpa,pod \\

&#x20;     -n myapp-demo



查看 Status：



&#x20;   kubectl get myapp helm-demo \\

&#x20;     -n myapp-demo \\

&#x20;     -o yaml



查看 Events：



&#x20;   kubectl get events \\

&#x20;     -n myapp-demo \\

&#x20;     --field-selector involvedObject.name=helm-demo



\---



\# 本地开发



\## 安装 CRD



&#x20;   make install



\## 本地启动 Controller



&#x20;   make run



\## 执行测试



&#x20;   make test



当前 Controller 测试覆盖率约为：



&#x20;   77.2%



测试覆盖：



\- Deployment 创建与更新

\- Reconcile 幂等性

\- Deployment 删除重建

\- Deployment Drift

\- Service Drift

\- ConfigMap Drift

\- HPA Drift

\- HPA safe defaults

\- Status / Conditions

\- Transient Error

\- Permanent Error

\- Retry Policy

\- Fail Reconcile 行为



\---



\# 技术栈



\- Go

\- Kubernetes

\- Kubebuilder

\- controller-runtime

\- client-go

\- CustomResourceDefinition

\- Reconcile Pattern

\- OwnerReference

\- Finalizer

\- HorizontalPodAutoscaler

\- Prometheus Metrics

\- Helm

\- Docker

\- K3s

\- EnvTest

\- GitHub Actions



\---



\# 开发与验证环境



当前主要开发与实机验证环境：



\- Windows 11

\- WSL2 Ubuntu 24.04

\- Go 1.26

\- K3s v1.35.5+k3s1

\- Kubebuilder 4.15

\- controller-gen 0.21

\- Helm 3

\- Docker Desktop



\---



\# 项目结构



&#x20;   .

&#x20;   ├── api/

&#x20;   │   └── v1/

&#x20;   ├── charts/

&#x20;   │   └── myapp-operator/

&#x20;   ├── cmd/

&#x20;   │   └── main.go

&#x20;   ├── config/

&#x20;   │   ├── crd/

&#x20;   │   ├── manager/

&#x20;   │   ├── rbac/

&#x20;   │   └── samples/

&#x20;   ├── docs/

&#x20;   ├── internal/

&#x20;   │   └── controller/

&#x20;   ├── Dockerfile

&#x20;   ├── Makefile

&#x20;   └── README.md



\---



\# Validation



项目保留真实集群验收记录，用于记录故障注入、运行结果和自愈行为。



\## Deployment Self-Healing



\[Deployment Drift Detection and Self-Healing](docs/deployment-self-healing-validation.md)



主要验证：



\- Deployment Drift Detection

\- Image Drift

\- Labels Drift

\- Strategy Drift

\- Controller 自动自愈



\## Helm Delivery \& Runtime Self-Healing



\[Helm 交付与运行时自愈验收](docs/helm-delivery-validation.md)



主要验证：



\- Helm Chart

\- Docker Image

\- K3s containerd

\- Controller Deployment

\- Leader Election

\- CRD Schema Upgrade

\- Deployment / Service / ConfigMap / HPA 创建

\- 四类资源 Drift Self-Healing

\- HPA Replica Ownership

\- Optimistic Concurrency

\- WSL2 / Docker Desktop / K3s 运行时故障定位



\---



\# 项目定位



本项目用于学习和实践 Kubernetes Operator / Platform Engineering 中的核心控制模式。



当前重点覆盖：



\- Declarative API

\- Desired State / Actual State

\- Control Loop

\- Idempotent Reconcile

\- Drift Detection

\- Self-Healing

\- Resource Ownership

\- Status Conditions

\- Retry Policy

\- Observability

\- Helm Delivery



项目仍以学习、工程实践和面试展示为主要目标，并不宣称已经达到完整生产级 Kubernetes Operator 的全部要求。
