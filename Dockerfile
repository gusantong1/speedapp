# syntax=docker/dockerfile:1
FROM golang:1.22-bookworm AS gobuilder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY app/packager ./app/packager
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /packager ./app/packager

FROM node:20-bookworm
ENV DEBIAN_FRONTEND=noninteractive \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    ANDROID_HOME=/opt/android-sdk \
    JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64 \
    GRADLE_USER_HOME=/app/gradle-cache \
    PACK_HOST=0.0.0.0 \
    PACK_PROJECT_ROOT=/app \
    TZ=Etc/UTC
RUN apt-get update && apt-get install -y --no-install-recommends \
    openjdk-17-jdk-headless wget unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# 保留项目原有 Google SDK 版本；许可证仅在构建环境中接受。
RUN mkdir -p "${ANDROID_SDK_ROOT}/cmdline-tools" \
    && wget -q https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip -O /tmp/cmdline-tools.zip \
    && unzip -q /tmp/cmdline-tools.zip -d "${ANDROID_SDK_ROOT}/cmdline-tools" \
    && mv "${ANDROID_SDK_ROOT}/cmdline-tools/cmdline-tools" "${ANDROID_SDK_ROOT}/cmdline-tools/latest" \
    && rm /tmp/cmdline-tools.zip \
    && yes | "${ANDROID_SDK_ROOT}/cmdline-tools/latest/bin/sdkmanager" --licenses >/dev/null \
    && "${ANDROID_SDK_ROOT}/cmdline-tools/latest/bin/sdkmanager" "platform-tools" "platforms;android-33" "build-tools;33.0.2"
WORKDIR /app
COPY index.js ./
COPY app/build.gradle.kts app/proguard-rules.pro ./app/
COPY app/src ./app/src
COPY gradlew gradlew.bat gradle.properties settings.gradle.kts build.gradle.kts ./
COPY gradle ./gradle
COPY app/packager/etc/packager-etc.yaml ./etc/packager.yaml
COPY --from=gobuilder /packager /app/packager
ENV ANDROID_USER_HOME=/app/data/android-user-home
RUN useradd --uid 10001 --create-home packapp \
    && sed -i 's/\r$//' gradlew \
    && chmod +x gradlew packager \
    && mkdir -p data gradle-cache .gradle secrets \
    && chown -R packapp:packapp data gradle-cache .gradle secrets
USER packapp
EXPOSE 10010
VOLUME ["/app/data", "/app/gradle-cache"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/packager", "-f", "/app/etc/packager.yaml", "-healthcheck"]
ENTRYPOINT ["/app/packager"]
CMD ["-f", "/app/etc/packager.yaml"]
