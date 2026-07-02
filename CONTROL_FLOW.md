# Architecture & Control Flow

The plugin's component structure, then how control flows through it from process
start to container allocation. Diagrams are Mermaid (rendered natively by
GitHub). Mirrors the code in
[cmd/tt-device-plugin/main.go](cmd/tt-device-plugin/main.go),
[internal/plugin/plugin.go](internal/plugin/plugin.go),
[internal/device/discover.go](internal/device/discover.go), and
[internal/cdi/cdi.go](internal/cdi/cdi.go).

## Components

Package structure and dependencies. Arrows are "depends on / talks to".

```mermaid
flowchart TB
  subgraph HOST["Host / node"]
    DEVFS[("/dev/tenstorrent")]
    SYSFS[("/sys/class/tenstorrent")]
  end

  subgraph BIN["tt-device-plugin binary"]
    MAIN["cmd/tt-device-plugin<br/>main · startPlugins · watchKubelet"]
    subgraph INT["internal packages"]
      DEV["device<br/>Discover · Heartbeat<br/>Temperature · MaxTemperature"]
      CDIP["cdi<br/>WriteSpec · Kind · QualifiedName"]
      PLG["plugin<br/>Plugin = DevicePluginServer<br/>Run · serve · register<br/>ListAndWatch · Allocate · checkHealth"]
    end
  end

  subgraph K8S["Cluster"]
    KUBELET["kubelet<br/>Registration + DevicePlugin gRPC"]
    RT["containerd 2.x"]
  end

  CDISPEC[("/var/run/cdi/*.json")]

  MAIN --> DEV
  MAIN --> CDIP
  MAIN --> PLG
  PLG --> DEV
  PLG --> CDIP
  CDIP --> DEV

  DEV -->|reads| DEVFS
  DEV -->|reads| SYSFS
  CDIP -->|writes| CDISPEC
  PLG <-->|"gRPC over unix socket"| KUBELET
  KUBELET -->|CDIDevices via CRI| RT
  RT -->|reads| CDISPEC
```

**Responsibilities:**

| Component | Role |
|-----------|------|
| `cmd/tt-device-plugin` | Orchestration: discovery, per-class plugin startup, kubelet-restart watch, shutdown. Owns no device logic. |
| `internal/device` | The only package that touches the host: discovery + sysfs reads (card type, NUMA, hwmon, heartbeat, temperature). Leaf package. |
| `internal/cdi` | Generates CDI specs from `device.Device` data. Depends on `device`, not on `plugin`. |
| `internal/plugin` | The gRPC `DevicePluginServer`: lifecycle, registration, `ListAndWatch`, `Allocate` (CDI or legacy), health. Depends on `device` + `cdi`. |

Deployed as a DaemonSet via the Helm chart in [helm/tt-device-plugin](helm/tt-device-plugin);
`device`/`sys`/`cdi` host paths are mounted in (see [PREREQUISITES.md](PREREQUISITES.md)).

## 1. Process startup & lifecycle

```mermaid
flowchart TD
  START([main]) --> INIT["klog init<br/>signal.NotifyContext SIGINT/SIGTERM"]
  INIT --> SP["startPlugins(ctx)"]

  subgraph SPB["startPlugins"]
    DISC["device.Discover()<br/>scan /dev/tenstorrent + /sys"]
    DISC --> CHK{"devices found?"}
    CHK -->|no / err| FATAL["klog.Fatalf — exit"]
    CHK -->|yes| CDIQ{"TT_CDI_ENABLED?"}
    CDIQ -->|yes| WSPEC["cdi.WriteSpec per class<br/>→ /var/run/cdi"]
    CDIQ -->|no| LOOP
    WSPEC --> LOOP["for each resource class"]
    LOOP --> NEW["plugin.New(class, devs)"]
    NEW --> GORUN["go p.Run(ctx)"]
  end

  SP --> WATCH["go watchKubelet(ctx, restart)"]
  WATCH --> BLOCK["&lt;-ctx.Done()"]
  BLOCK --> SHUT["Shutting down:<br/>p.Stop() for each plugin"]
  SHUT --> END([exit])

  GORUN -.spawns.-> RUNREF["Run() — see section 2"]
  WATCH -.on kubelet.sock recreated.-> RSTREF["restart — see section 5"]
```

## 2. Per-plugin Run() — serve, wait, register

```mermaid
flowchart TD
  RUN([p.Run ctx]) --> CTXQ{"ctx already<br/>cancelled?"}
  CTXQ -->|yes| RET1["return ctx.Err()"]
  CTXQ -->|no| RM["removeSocket(stale sock)"]
  RM --> SERVE["serve()"]

  subgraph SVB["serve"]
    LIS["net.Listen unix sock"]
    LIS --> GS["grpc.NewServer<br/>RegisterDevicePluginServer"]
    GS --> STORE["store grpcServer under mu"]
    STORE --> GOSERVE["go srv.Serve(lis)<br/>(filters ErrServerStopped)"]
  end

  SERVE --> WR["waitReady(ctx)"]
  WR --> WRL{"dial own sock<br/>succeeds?"}
  WRL -->|no, retry 100ms| WRL
  WRL -->|timeout 5s| RET2["return error"]
  WRL -->|yes| REG["register(ctx)"]

  REG --> REGC["grpc client → kubelet.sock<br/>Register RPC (10s timeout)"]
  REGC -->|err| RET3["return error"]
  REGC -->|ok| LOGS["log: Serving resourceName"]
  LOGS --> SEL{"select"}
  SEL -->|ctx.Done| RETOK["return nil"]
  SEL -->|stop closed| RETOK
```

## 3. gRPC serving — ListAndWatch & Allocate

Once registered, the kubelet drives two main RPCs.

```mermaid
flowchart TD
  subgraph LW["ListAndWatch (stream)"]
    LW0["send buildDeviceList()<br/>(runs checkHealth per device)"]
    LW0 --> TICK["ticker 30s"]
    TICK --> TSEL{"select"}
    TSEL -->|stop| LWEND["return nil"]
    TSEL -->|tick| LWSEND["re-send buildDeviceList()<br/>updated health"]
    LWSEND --> TSEL
  end

  subgraph AL["Allocate (per container request)"]
    A0["for each requested device id"]
    A0 --> AV{"id in byID map?"}
    AV -->|no| AERR["return InvalidArgument"]
    AV -->|yes| ACC["collect id"]
    ACC --> AENV["set Envs:<br/>TT_VISIBLE_DEVICES = joined ids"]
    AENV --> AMODE{"useCDI?"}
    AMODE -->|yes| ACDI["resp.CdiDevices =<br/>tenstorrent.com/class=id"]
    AMODE -->|no| ALEG["resp.Devices = deviceSpecs()<br/>resp.Mounts = legacyMounts()<br/>(/sys ro + hugepages)"]
    ACDI --> ADONE["append ContainerAllocateResponse"]
    ALEG --> ADONE
  end
```

## 4. checkHealth decision flow

Called for every device on each `buildDeviceList()` (initial send + every 30s),
so health is continuously re-evaluated and **recovers** automatically.

```mermaid
flowchart TD
  H([checkHealth dev]) --> HW{"HwmonDir set?"}
  HW -->|no| HB
  HW -->|yes| TEMP["device.Temperature()<br/>read temp1_input"]
  TEMP --> TREAD{"readable?"}
  TREAD -->|no| UNH1["Unhealthy<br/>(sensor unreadable)"]
  TREAD -->|yes| LIM{"tempMaxMilliC override set?"}
  LIM -->|yes| CMP
  LIM -->|no| SYS["limit = sysfs temp1_max<br/>(if present)"]
  SYS --> CMP{"temp >= limit > 0?"}
  CMP -->|yes| UNH2["Unhealthy<br/>(over temperature)"]
  CMP -->|no| HB{"SysfsDir set?"}

  HB -->|no| OK["Healthy"]
  HB -->|yes| HBR["device.Heartbeat()<br/>read tt_heartbeat"]
  HBR --> HBP{"prev == current?"}
  HBP -->|yes| UNH3["Unhealthy<br/>(heartbeat stalled)"]
  HBP -->|no| OK
```

## 5. Kubelet restart & shutdown

```mermaid
flowchart TD
  subgraph WK["watchKubelet (goroutine)"]
    FS["fsnotify watch<br/>/var/lib/kubelet/device-plugins"]
    FS --> EV{"event"}
    EV -->|ctx.Done| WKEND["return"]
    EV -->|kubelet.sock Created| RCB["restart callback"]
    RCB --> STOPALL["p.Stop() for each plugin"]
    STOPALL --> RESP["startPlugins(ctx) again<br/>(re-discover, re-register)"]
    RESP --> FS
  end

  subgraph STOP["p.Stop() (idempotent)"]
    C1{"stop already closed?"}
    C1 -->|yes| SRET["return"]
    C1 -->|no| CLOSE["close(stop)"]
    CLOSE --> GRACE["GracefulStop<br/>(force Stop after 5s)"]
    GRACE --> RMS["removeSocket"]
  end
```

## Key invariants

- **Serve before register** — `waitReady` polls the plugin's own socket so the
  gRPC server is accepting before `Register` is called (avoids a registration
  race).
- **Health is pull-based and recovers** — every 30s tick re-runs `checkHealth`;
  an unhealthy device returns to `Healthy` once the condition clears.
- **`Stop()` is idempotent and mutex-guarded** — safe to call from the kubelet
  restart path and the shutdown path.
- **CDI vs legacy is chosen per Allocate** via `useCDI`; `TT_VISIBLE_DEVICES` is
  always set from the request in both modes.
