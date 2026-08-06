\# Deployment Drift Detection and Self-Healing Validation



\## Overview



This document records the automated test and K3s runtime validation of the Deployment drift detection and self-healing capability implemented by the MyApp Operator.



Validation date: 2026-08-06



Related commits:



\* `0ef404f` — `feat: add deployment drift detection and self-healing`

\* `6ff2943` — `merge: add deployment drift detection and self-healing`



\## Purpose



The MyApp Operator continuously compares the desired state declared in the `MyApp` custom resource with the actual state of the managed Deployment.



When a managed Deployment is modified manually, the Controller detects the drift and restores the fields controlled by the Operator.



The following fields are managed:



\* Deployment replicas when HPA is disabled

\* Deployment labels

\* Pod template labels

\* Container name

\* Container image

\* Container ports

\* ConfigMap environment references

\* Container resource requests

\* Deployment rollout strategy



When HPA is enabled, the Controller does not force the Deployment replica count back to `MyApp.spec.replicas`. This prevents the Operator and HPA from continuously overwriting each other.



\## Automated Test



The envtest suite performs the following workflow:



1\. Creates a valid `MyApp` custom resource.

2\. Reconciles the resource.

3\. Verifies that the Deployment is created correctly.

4\. Manually changes the managed Deployment.

5\. Runs Reconcile again.

6\. Verifies that the Deployment is restored to the desired state.



The test introduces drift in:



\* Replica count

\* Deployment labels

\* Pod template labels

\* Container image

\* Container ports

\* ConfigMap `EnvFrom`

\* Deployment update strategy



Test result:



```text

ok      k8s-myapp-operator/internal/controller  6.896s  coverage: 58.9% of statements

```



The Controller test coverage increased from 39.7% to 58.9%.



\## K3s Runtime Environment



The feature was also validated against a real K3s cluster.



Managed resource:



```text

Namespace: default

MyApp: my-app-example

Phase: Running

Ready replicas: 2

Desired replicas: 2

Image: nginx:latest

```



Managed Deployment before drift:



```text

Image: nginx:latest

Deployment labels: {"app":"my-app-example"}

Pod labels: {"app":"my-app-example"}

Strategy: RollingUpdate

```



The `MyApp` resource had HPA enabled:



```text

HPA: my-app-example-hpa

Minimum replicas: 2

Maximum replicas: 10

CPU target: 80%

```



Because HPA was enabled, replica drift was intentionally excluded from the runtime validation.



\## Drift Injection



The managed Deployment was manually modified with:



\* Container image changed to `busybox:latest`

\* Additional Deployment label added

\* Additional Pod template label added

\* Deployment strategy changed from `RollingUpdate` to `Recreate`



The Controller watched the owned Deployment and triggered reconciliation immediately.



\## Self-Healing Result



The Deployment was automatically restored to:



```text

Image: nginx:latest

Deployment labels: {"app":"my-app-example"}

Pod labels: {"app":"my-app-example"}

Strategy: RollingUpdate

```



The following Kubernetes Events were generated:



```text

Normal  DeploymentDriftCorrected  Corrected Deployment drift in fields: \[deploymentLabels]

Normal  DeploymentDriftCorrected  Corrected Deployment drift in fields: \[image]

Normal  DeploymentDriftCorrected  Corrected Deployment drift in fields: \[podLabels strategy]

Normal  Ready                     MyApp is ready with 2/2 replicas

```



\## Validation Conclusion



The validation confirms that the MyApp Operator:



\* Detects manual changes to managed Deployments

\* Restores fields to the state declared by the MyApp resource

\* Avoids replica ownership conflicts with HPA

\* Emits Kubernetes Events describing corrected fields

\* Returns the MyApp resource to the Ready state after recovery

\* Supports both envtest verification and real K3s runtime validation



This demonstrates the declarative control-loop and self-healing behavior expected from a Kubernetes Operator.



