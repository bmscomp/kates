# Native Image Runbook

How to build, verify and debug the ahead-of-time compiled Kates backend.

Read this before changing anything that Jackson serializes, anything loaded from
the classpath at runtime, or anything that touches a JNI library. Those three
categories are where native builds break, and they break in a way the JVM test
suite cannot see.

## Why native needs its own attention

The JVM resolves everything at runtime: reflection finds getters, the
classloader finds resources, JNI libraries load on demand. GraalVM decides all
of it at build time, from static analysis. Whatever the analysis cannot see is
not in the binary.

That gives three failure modes, none of which fail the JVM tests:

| What broke | How it looks in native | How it looks on the JVM |
|---|---|---|
| A DTO is not registered for reflection | Endpoint returns `{}`, or throws `InvalidDefinitionException: No serializer found` | Correct JSON |
| A classpath resource is not in the image | The feature is silently empty — the playbook catalog returns `[]` | Works |
| A JNI library is missing or initialized too early | `UnsatisfiedLinkError` the first time compression is used, minutes into a benchmark | Works |

The last one is the nastiest: it surfaces during a long test run, not at
startup, so it looks like a Kafka problem rather than a packaging one.

## Building

```bash
# From the working tree, through the same Dockerfile the release uses
make kates-image-native-local

# Or directly
docker build -f kates/Dockerfile.native -t kates:native-local .
```

The compiler needs roughly **8 GB of memory** and 15–25 minutes on four cores.
On Docker Desktop, raise the VM memory limit first — below about 6 GB the build
dies with an opaque OOM kill that reads like a compiler crash.

Two build settings are load-bearing and set in `Dockerfile.native`:

- `-Dquarkus.native.native-image-xmx=8g` — memory for the compiler. It must go
  through this property, **not** `additional-build-args`: that key already
  carries the `--initialize-at-run-time` list from `application.properties`, and
  passing it on the command line replaces the whole value rather than appending
  to it.
- `-Dquarkus.native.march=compatibility` — without it the compiler targets the
  build machine's CPU, and the binary dies with `SIGILL` on older hardware.

## Verifying

```bash
make kates-native-smoke                       # against kates:native-local
IMAGE=kates:native-ci make kates-native-smoke  # or any other tag
```

`scripts/native-smoke-test.sh` boots the binary against a throwaway Postgres and
calls the paths where AOT breakage shows up: liveness, readiness (which proves
Flyway ran, including the non-transactional V20), OpenAPI, the playbook catalog
(classpath YAML), and a domain payload (reflection). CI runs the same script on
every PR that touches `kates/**`, plus nightly — see
`.github/workflows/native.yml`.

## Keeping reflection registration honest

`NativeReflectionConfig` and `NativePayloadReflectionConfig` list the types that
cross the wire. `NativeReflectionRegistryTest` walks the compiled payload
packages and fails the build when a class is missing from them, so you find out
in the unit suite instead of in production.

When it fails, add the class to `NativePayloadReflectionConfig`. If the class
genuinely never leaves the process, add it to that test's `NOT_SERIALIZED` set
with a one-line reason instead.

Registration is needed because almost every endpoint here returns
`Response.ok(...)`. Quarkus registers types it can see in a resource method's
signature; an opaque `Response` tells it nothing.

## Adding a classpath resource

Anything read with `getResourceAsStream` at runtime must be matched by
`quarkus.native.resources.includes` in `application.properties`, or it will not
be in the image. Current entries and why:

| Pattern | Why |
|---|---|
| `linux/**/*.so`, `darwin/**/*.dylib`, `org/xerial/snappy/native/**`, `net/jpountz/**`, `com/github/luben/zstd/**` | Compression JNI libraries, extracted at runtime |
| `db/migration/*.conf` | Flyway script config sidecars — V20 must run outside a transaction |
| `playbooks/*.yaml` | The disruption playbook catalog |

A missing pattern is not an error at build time. The feature just comes up
empty, which is why the smoke test asserts on the playbook catalog specifically.

## Debugging a failure

**The build fails with "Classes that should be initialized at run time got
initialized during image building"** — a class ran its static initializer during
the build, usually because it loads a native library or seeds a `SecureRandom`.
Add it to the `--initialize-at-run-time` list in
`quarkus.native.additional-build-args`. The error names the offending class and
the chain that reached it; read the chain, since the root cause is often the
class that *touched* it, not the one named.

**An endpoint returns `{}` or 500 with "No serializer found"** — the response
type is not registered. Add it to `NativePayloadReflectionConfig`;
`NativeReflectionRegistryTest` should have caught it, so also check whether the
class lives outside the packages that test scans.

**`UnsatisfiedLinkError` during a benchmark** — a compression library was not
extracted into `/app/lib`. The Dockerfile's extraction step has a guard that
fails the build when zstd, lz4 or snappy is missing; if it passed and the error
still happens, check `LD_LIBRARY_PATH` in the runtime stage.

**The container is killed shortly after start** — the native image ignores
`JAVA_TOOL_OPTIONS`, so the chart's `jvm.options` does nothing here. Heap is
governed by the image's `CMD` (`-XX:MaximumHeapSizePercent=75`), overridable
with `args:` on the container. Use `charts/kates/values-native.yaml`, which
empties `jvm.options` and sizes resources for a native binary.

## Running it

```bash
helm upgrade --install kates charts/kates -n kates \
  -f charts/kates/values-native.yaml
```

Images are published as `<appVersion>-native` alongside the JVM tag by
`.github/workflows/publish-docker.yml` on a `v*` tag.
