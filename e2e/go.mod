module netbird-forwarder/e2e

go 1.26.0

require (
	github.com/netbirdio/netbird v0.81.5
	github.com/stretchr/testify v1.12.1
)

require (
	dario.cat/mergo v1.0.2 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v0.2.1 // indirect
	github.com/cpuguy83/dockercfg v0.3.2 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/docker v28.0.1+incompatible // indirect
	github.com/docker/go-connections v0.6.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/ebitengine/purego v0.9.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/lufia/plan9stats v0.0.0-20240513124658-fba389f38bae // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/patternmatcher v0.6.0 // indirect
	github.com/moby/sys/sequential v0.5.0 // indirect
	github.com/moby/sys/user v0.3.0 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/oapi-codegen/runtime v1.1.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/shirou/gopsutil/v4 v4.25.8 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/testcontainers/testcontainers-go v0.37.0 // indirect
	github.com/tklauser/go-sysconf v0.3.15 // indirect
	github.com/tklauser/numcpus v0.10.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

// Same fork as the forwarder: its e2e harness builds the management server and
// the client from the source the forwarder is compiled against.
replace github.com/netbirdio/netbird => github.com/wouter0100/netbird v0.81.6-0.20261116113010-e94fb7022ead

// Mirrors the replace set of netbirdio/netbird v0.81.5. A dependency's own
// replace directives are ignored, so they have to be restated here or the
// client does not build.
replace github.com/kardianos/service => github.com/netbirdio/service v0.0.0-20240911161631-f62744f42502

replace github.com/getlantern/systray => github.com/netbirdio/systray v0.0.0-20231030152038-ef1ed2a27949

replace golang.zx2c4.com/wireguard => github.com/netbirdio/wireguard-go v0.0.0-20261008122457-d2b1ce9bfee9

replace github.com/cloudflare/circl => codeberg.org/cunicu/circl v0.0.0-20230801113412-fec58fc7b5f6

replace github.com/pion/ice/v4 => github.com/netbirdio/ice/v4 v4.0.0-20250908184934-6202be846b51

replace github.com/dexidp/dex => github.com/netbirdio/dex v0.244.1-0.20260716205454-a163de3129e5

replace github.com/dexidp/dex/api/v2 => github.com/netbirdio/dex/api/v2 v2.0.0-20260512110716-8d70ad8647c1

replace github.com/mailru/easyjson => github.com/netbirdio/easyjson v0.9.0

replace github.com/wailsapp/wails/v3 => github.com/netbirdio/wails/v3 v3.0.0-beta.3.0.20261009120932-4e087a8bfd6d
