# Stage 1: Build the KATES CLI (Go)
# Base images pinned by digest for reproducibility (tag kept for readability).
# Digests are the multi-arch manifest-list digests; refresh with Renovate/Dependabot.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:8d22e29d960bc50cd025d93d5b7c7d220b1ee9aa7a239b3c8f55a57e987e8d45 AS cli-builder
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

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD curl -f http://localhost:8080/api/health || exit 1

ENTRYPOINT ["java", "-jar", "quarkus-run.jar"]
