# 在 speedapp 目录构建：
#   docker build -t speedapp-packager .
#
# 运行：
#   docker run -d --name speedapp-packager \
#     -p 10010:10010 \
#     -v /your/host/packager.yaml:/app/etc/packager.yaml:ro \
#     -v /your/host/henry20230831114241-keystore.jks:/app/app/henry20230831114241-keystore.jks:ro \
#     speedapp-packager

FROM golang:1.22-bookworm AS gobuilder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -ldflags="-s -w" -o packager ./cmd/packager/

FROM node:20-bookworm

ENV DEBIAN_FRONTEND=noninteractive \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    ANDROID_HOME=/opt/android-sdk \
    JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64

RUN apt-get update && apt-get install -y --no-install-recommends \
    openjdk-17-jdk-headless \
    wget unzip ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p "${ANDROID_SDK_ROOT}/cmdline-tools" \
    && wget -q https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip -O /tmp/cmdline-tools.zip \
    && unzip -q /tmp/cmdline-tools.zip -d "${ANDROID_SDK_ROOT}/cmdline-tools" \
    && mv "${ANDROID_SDK_ROOT}/cmdline-tools/cmdline-tools" "${ANDROID_SDK_ROOT}/cmdline-tools/latest" \
    && rm /tmp/cmdline-tools.zip \
    && yes | "${ANDROID_SDK_ROOT}/cmdline-tools/latest/bin/sdkmanager" --licenses \
    && "${ANDROID_SDK_ROOT}/cmdline-tools/latest/bin/sdkmanager" \
        "platform-tools" \
        "platforms;android-33" \
        "build-tools;33.0.2"

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci --omit=dev

COPY index.js ./
RUN wget -q https://github.com/iBotPeaches/Apktool/releases/download/v2.9.3/apktool_2.9.3.jar -O apktool.jar

COPY app ./app
COPY gradlew gradlew.bat gradle.properties settings.gradle.kts build.gradle.kts ./
COPY gradle ./gradle

RUN chmod +x gradlew \
    && mkdir -p data/agent_apks .cache

COPY --from=gobuilder /build/packager /app/packager
COPY etc/packager.yaml /app/etc/packager.yaml

ENV TZ=Asia/Shanghai
EXPOSE 10010

ENTRYPOINT ["/app/packager"]
CMD ["-f", "/app/etc/packager.yaml"]
