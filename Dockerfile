# syntax=docker/dockerfile:1

# The release workflow supplies the version-matched, multi-platform server
# image. BuildKit selects the server image for the current target platform.
# The placeholder keeps the Dockerfile syntactically valid while ensuring a
# build that omits the versioned image argument cannot silently use "latest".
ARG WEEWOO_SERVER_IMAGE=invalid.invalid/weewoo-server:release-tag-required
FROM ${WEEWOO_SERVER_IMAGE} AS weewoo-server

# Define the public runtime independently from the server build image.
FROM gcr.io/distroless/base-debian11 AS public-release
WORKDIR /app
COPY --from=weewoo-server --chown=nonroot:nonroot /app/weewoo-server ./weewoo-server
COPY --from=weewoo-server --chown=nonroot:nonroot /app/jecdf ./jecdf
COPY --from=weewoo-server --chown=nonroot:nonroot /app/ui/dist ./ui/dist
COPY --from=weewoo-server --chown=nonroot:nonroot /var/lib/weewoo /var/lib/weewoo

EXPOSE 8080 5000
VOLUME ["/var/lib/weewoo"]
USER nonroot:nonroot
ENTRYPOINT ["./weewoo-server"]
