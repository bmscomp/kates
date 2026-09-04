# Stage 1: Build the KATES CLI (Go)
# Base images pinned by digest for reproducibility (tag kept for readability).
# Digests are the multi-arch manifest-list digests; refresh with Renovate/Dependabot.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS cli-builder
ENV GOTOOLCHAIN=auto
WORKDIR /cli
COPY cli/go.mod cli/go.sum ./
RUN go mod download
COPY cli/ ./
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
  -trimpath \
  -ldflags="-s -w \
  -X github.com/bmscomp/kates/cli/cmd.Version=1.0.0 \
  -X github.com/bmscomp/kates/cli/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o /kates .

# Stage 2: Build the Java application
FROM --platform=$BUILDPLATFORM eclipse-temurin:21-jdk@sha256:da9d3a4f7650db39b918fc5a2c3da76556fb8cc8e5f3767cdea0bb409286951a AS builder
WORKDIR /build

COPY kates/pom.xml .
COPY kates/.mvn .mvn
COPY kates/mvnw .
RUN --mount=type=cache,target=/root/.m2 \
  chmod +x mvnw && ./mvnw dependency:go-offline -B 2>/dev/null || true

COPY kates/src src
RUN --mount=type=cache,target=/root/.m2 \
  ./mvnw package -DskipTests -Dnet.bytebuddy.experimental=true -B

# Stage 3: Runtime — Java app + CLI on PATH
FROM eclipse-temurin:21-jre@sha256:273396ed5998598ed1091e8d72711c2d36980a0e65103859c55a4e977a41ffd3
WORKDIR /app

COPY --from=builder /build/target/quarkus-app/ ./
COPY --from=cli-builder /kates /usr/local/bin/kates

RUN addgroup --system kates && adduser --system --ingroup kates --home /home/kates kates \
  && mkdir -p /home/kates \
  && printf 'current-context: local\ncontexts:\n  local:\n    url: http://localhost:8080\n    output: table\n' > /home/kates/.kates.yaml \
  && chown -R kates:kates /home/kates

USER kates
ENV HOME=/home/kates

EXPOSE 8080

# /q/health/live, not /api/health: the latter makes a live Kafka AdminClient
# call per request, so it blows the 3s timeout and reports the container
# unhealthy whenever the broker under test is slow or down — which for a Kafka
# testing tool is a normal, expected state, not a broken container.
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD curl -f http://localhost:8080/q/health/live || exit 1

ENTRYPOINT ["java", "-jar", "quarkus-run.jar"]
