ARG GO_VERSION=1.25.8
ARG RUBY_VERSION=4.0.0

FROM docker.io/library/golang:${GO_VERSION}-bookworm AS go

FROM docker.io/library/ruby:${RUBY_VERSION}-slim

WORKDIR /workspace

LABEL org.opencontainers.image.source="https://github.com/devopsellence/devopsellence"

COPY --from=go /usr/local/go /usr/local/go

RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y \
      bash \
      build-essential \
      ca-certificates \
      curl \
      docker-cli \
      git \
      libpq-dev \
      libpq5 \
      libvips \
      libyaml-dev \
      pkg-config && \
    rm -rf /var/lib/apt/lists /var/cache/apt/archives

ENV PATH="/usr/local/go/bin:${PATH}" \
    BUNDLE_PATH="/usr/local/bundle" \
    BUNDLE_JOBS="4" \
    BUNDLE_RETRY="3" \
    BUNDLE_WITHOUT=""

COPY Gemfile Gemfile.lock ./
COPY vendor ./vendor

RUN bundle install

CMD ["bash"]
