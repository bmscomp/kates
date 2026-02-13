# JVM build
FROM eclipse-temurin:25-jdk AS builder
WORKDIR /build

COPY pom.xml .
COPY .mvn .mvn
COPY mvnw .
RUN chmod +x mvnw && ./mvnw dependency:go-offline -B 2>/dev/null || true

COPY src src
RUN ./mvnw package -DskipTests -B

# Runtime
FROM eclipse-temurin:25-jre
WORKDIR /app

COPY --from=builder /build/target/quarkus-app/ ./

RUN addgroup --system kates && adduser --system --ingroup kates kates
USER kates

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD curl -f http://localhost:8080/api/health || exit 1

ENTRYPOINT ["java", "-jar", "quarkus-run.jar"]
