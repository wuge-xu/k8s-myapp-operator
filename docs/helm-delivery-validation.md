\# Helm 交付与运行时自愈验收



\## 1. 验收信息



验收日期：2026-08-13



验收环境：



\- Windows 11 + WSL2 Ubuntu

\- K3s v1.35.5+k3s1

\- Helm v3

\- Docker Desktop

\- MyApp Operator Helm Chart 0.1.0

\- Controller 镜像：k8s-myapp-operator:dev



本次验收目标是验证 MyApp Operator 不仅能够通过源码方式运行，还能够形成完整的 Kubernetes 交付链路：



Source Code

→ Docker Image

→ K3s containerd

→ Helm Chart

→ RBAC

→ Controller Deployment

→ MyApp Reconcile

→ Drift Detection

→ Self-Healing



\---



\## 2. Helm Chart 验证



Helm Chart 位于：



&#x20;   charts/myapp-operator



Chart 包含：



\- CRD

\- ServiceAccount

\- ClusterRole

\- ClusterRoleBinding

\- Leader Election Role

\- Leader Election RoleBinding

\- Controller Deployment

\- Metrics Service



执行 Helm lint：



&#x20;   helm lint charts/myapp-operator



验证结果：



&#x20;   1 chart(s) linted, 0 chart(s) failed



使用 Helm template 可以正常渲染：



\- ServiceAccount

\- ClusterRole

\- ClusterRoleBinding

\- Role

\- RoleBinding

\- Service

\- Deployment

\- CustomResourceDefinition



\---



\## 3. Controller 镜像交付



Controller 使用项目 Dockerfile 构建：



&#x20;   docker build -t k8s-myapp-operator:dev .



构建成功后生成本地镜像：



&#x20;   k8s-myapp-operator:dev



随后将镜像导入 K3s containerd：



&#x20;   sudo k3s ctr images import /tmp/k8s-myapp-operator-dev.tar



K3s image store 中能够正确识别：



&#x20;   docker.io/library/k8s-myapp-operator:dev



\---



\## 4. WSL2 / K3s 环境故障定位



在镜像导入阶段发现 K3s containerd socket 无法连接：



&#x20;   /run/k3s/containerd/containerd.sock: connect: connection refused



进一步检查发现 K3s kubelet 启动失败：



&#x20;   Failed to start ContainerManager

&#x20;   system validation failed - wrong number of fields (expected 6, got 7)



检查 `/proc/mounts` 后定位到 Docker Desktop WSL Integration 创建的异常挂载：



&#x20;   /Docker/host



该 mount record 被解析为 7 个字段，导致 Kubernetes mount validation 失败。



临时卸载该挂载：



&#x20;   sudo umount /Docker/host



再次启动 K3s 后：



&#x20;   k3s.service = active

&#x20;   Node = Ready



CRI 与 containerd 恢复正常。



该问题证明本次验收不仅覆盖 Operator 功能，也实际完成了一次 WSL2、Docker Desktop、K3s 与 containerd 运行时故障定位。



\---



\## 5. Helm 实际安装



使用 Helm 安装 Controller：



&#x20;   helm upgrade --install myapp-operator \\

&#x20;     charts/myapp-operator \\

&#x20;     --namespace myapp-operator-system \\

&#x20;     --create-namespace \\

&#x20;     --set image.repository=k8s-myapp-operator \\

&#x20;     --set image.tag=dev \\

&#x20;     --set image.pullPolicy=IfNotPresent



Helm Release：



&#x20;   NAME            STATUS

&#x20;   myapp-operator  deployed



Controller Deployment：



&#x20;   READY   AVAILABLE

&#x20;   1/1     1



Controller Pod：



&#x20;   READY   STATUS    RESTARTS

&#x20;   1/1     Running   0



Controller 成功完成 Leader Election：



&#x20;   Successfully acquired lease



Metrics Server：



&#x20;   bindAddress=:8080

&#x20;   secure=false



Health Probe：



&#x20;   :8081



\---



\## 6. CRD Schema 升级



集群中存在历史版本 MyApp CRD。



新 Controller 启动后发现：



&#x20;   unknown field "status.observedGeneration"



原因是 Helm `crds/` 不会自动升级已经存在的 CRD，集群中的旧 CRD schema 不包含最新的：



&#x20;   status.observedGeneration



手动应用当前 CRD：



&#x20;   kubectl apply -f config/crd/bases/app.demo.io\_myapps.yaml



更新后验证：



&#x20;   metadata.generation = 1

&#x20;   status.observedGeneration = 1

&#x20;   phase = Running



新 Reconcile 日志中不再出现：



&#x20;   unknown field



\---



\## 7. 独立 MyApp 验收实例



创建独立 Namespace：



&#x20;   myapp-demo



创建 MyApp：



&#x20;   helm-demo



Spec：



&#x20;   replicas: 2

&#x20;   image: nginx:1.27-alpine

&#x20;   port: 80



&#x20;   config:

&#x20;     APP\_ENV: helm-validation

&#x20;     DELIVERY\_MODE: helm



&#x20;   autoscaling:

&#x20;     enabled: true

&#x20;     minReplicas: 1

&#x20;     maxReplicas: 3

&#x20;     targetCPUUtilizationPercentage: 60



Controller 自动创建：



\- Deployment `helm-demo`

\- Service `helm-demo-service`

\- ConfigMap `helm-demo-config`

\- HPA `helm-demo-hpa`



对应 Kubernetes Events：



&#x20;   CreatedDeployment

&#x20;   CreatedService

&#x20;   CreatedConfigMap

&#x20;   CreatedHPA



Deployment 首次达到：



&#x20;   READY 2/2



MyApp Status：



&#x20;   phase: Running

&#x20;   readyReplicas: 2

&#x20;   observedGeneration: 1



Conditions：



&#x20;   Ready=True

&#x20;   Progressing=False

&#x20;   Degraded=False



\---



\## 8. Drift Self-Healing 验证



在不修改 MyApp Spec 的情况下，人为修改 Controller 管理的实际资源。



\### 8.1 Deployment Drift



将 Deployment image 人为修改为：



&#x20;   busybox:latest



Controller 自动恢复：



&#x20;   nginx:1.27-alpine



Event：



&#x20;   DeploymentDriftCorrected



修复字段：



&#x20;   \[image]



\### 8.2 Service Drift



将 Service selector 修改为错误值：



&#x20;   app=broken-demo



Controller 自动恢复：



&#x20;   app=helm-demo



Event：



&#x20;   ServiceDriftCorrected



修复字段：



&#x20;   \[selector]



\### 8.3 ConfigMap Drift



人为修改 ConfigMap：



&#x20;   APP\_ENV=broken

&#x20;   DELIVERY\_MODE=manual-drift



Controller 自动恢复：



&#x20;   APP\_ENV=helm-validation

&#x20;   DELIVERY\_MODE=helm



Event：



&#x20;   ConfigMapDriftCorrected



修复字段：



&#x20;   \[data]



\### 8.4 HPA Drift



人为将：



&#x20;   maxReplicas=99



Controller 自动恢复：



&#x20;   minReplicas=1

&#x20;   maxReplicas=3

&#x20;   targetCPUUtilizationPercentage=60



Event：



&#x20;   HPADriftCorrected



修复字段：



&#x20;   \[maxReplicas]



\---



\## 9. HPA 与 Operator 副本所有权验证



MyApp Spec 中声明：



&#x20;   replicas: 2



同时启用：



&#x20;   minReplicas: 1

&#x20;   maxReplicas: 3

&#x20;   targetCPUUtilizationPercentage: 60



当 CPU 使用率为：



&#x20;   0% / 60%



HPA 将 Deployment 从 2 个副本缩容至 1 个副本。



最终：



&#x20;   Deployment READY = 1/1

&#x20;   HPA replicas = 1

&#x20;   MyApp phase = Running

&#x20;   Ready=True



Controller 没有再次将 Deployment 强制恢复为 2 个副本。



这验证了：



> 当 HPA 启用时，MyApp Operator 不再持续控制 Deployment replicas 字段，由 HPA 接管运行时副本数量。



从而避免 Operator 与 HPA 之间发生控制循环和 replica ownership conflict。



\---



\## 10. Reconcile 并发冲突



HPA 调整副本期间观察到一次 Kubernetes optimistic concurrency conflict：



&#x20;   Operation cannot be fulfilled on myapps.app.demo.io "helm-demo":

&#x20;   the object has been modified



随后的 Reconcile 成功重新读取最新资源版本并最终收敛。



最终状态：



&#x20;   phase: Running

&#x20;   Ready=True

&#x20;   Progressing=False

&#x20;   Degraded=False



说明单次资源版本冲突不会破坏最终状态收敛。



\---



\## 11. 最终验收结果



本次实机验证通过以下能力：



\- Helm Chart 安装

\- CRD 安装与 schema 更新

\- Namespace 隔离部署

\- ServiceAccount 与 RBAC

\- Leader Election

\- Controller Deployment

\- Health Probe

\- Metrics Endpoint

\- Deployment Reconcile

\- Service Reconcile

\- ConfigMap Reconcile

\- HPA Reconcile

\- Status / Conditions

\- Kubernetes Events

\- Deployment Drift Self-Healing

\- Service Drift Self-Healing

\- ConfigMap Drift Self-Healing

\- HPA Drift Self-Healing

\- HPA Replica Ownership

\- Optimistic Concurrency 最终收敛

\- WSL2 / Docker Desktop / K3s 运行时故障定位



Helm Delivery 与运行时 Self-Healing 验收通过。

