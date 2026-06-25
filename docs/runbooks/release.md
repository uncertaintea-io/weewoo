# Release Procedures

## Release Automation

Once the software has reached a stable state, and you're ready to release:

```shell
# Ensure the working directory is clean:
git checkout main
git pull
git status

# Ensure dependencies are up-to-date:
go mod tidy
git diff  # if there are diffs, merge changes before continuing

# Sanity checks:
go test ./...
pre-commit run --all-files

# Choose a new version, use the scheme v${MAJOR}.${MINOR}.${PATCH}
# See references for a description of module version numbering.
TAG="v0.2.0"

# Create a tag for the release and push it.
# This helps prevent new PRs merged to main from sneaking into the release.
git tag ${TAG}
git push origin ${TAG}

# Trigger the release using the GitHub CLI:
gh release create ${TAG} --title ${TAG} --generate-notes
```

Creating a release triggers the `Publish Docker Image` GitHub Action. That
workflow builds the Docker image for `linux/amd64` and `linux/arm64`, then pushes
both `ghcr.io/uncertaintea-io/weewoo:${TAG}` and
`ghcr.io/uncertaintea-io/weewoo:latest`.
It also will generate and publish release notes based on the PRs included in the release.

## Manual Release Procedure

### Container Registry Access

We use the GitHub Container Registry (ghcr.io) to store our images.
In order to access the registry locally, you will need a GitHub Personal Access Token; specifically a "Personal Access Token (classic)".
GitHub's instructions are [here](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

I recommend storing this token in a `secrets/docker.token` file. If you do, lock the file down so only you can read it:

```bash
chmod 400 secrets/docker.token
```

You can then use the token to login to the container registry:

```bash
cat secrets/docker.token | docker login ghcr.io -u ${GITHUB_USER} --password-stdin
```

### Docker buildx

In order to build the multi-platform images we need, you will need to have Docker's `buildx` extension installed and configured.
You can see if you already have it by running:

```shell
docker buildx version
docker buildx ls
```

When you run that last command, you should see a "multiarch" configuration.
If you don't, you can set it up as follows:

```shell
docker buildx create --name multiarch --driver docker-container --use
docker buildx inspect --bootstrap
```

### Building the Docker Image

The GitHub Action normally handles this after a release is published. If you need
to push an image manually, run this in the root directory of the repo:

```shell
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/uncertaintea-io/weewoo:latest \
  --push .
```

After the image is pushed, you can inspect the image's manifest to see if everything looks okay:

```shell
docker buildx imagetools inspect ghcr.io/uncertaintea-io/weewoo:latest
```

## References

* [Module version numbering](https://go.dev/doc/modules/version-numbers)
* [Publishing a module](https://go.dev/doc/modules/publishing)
* [Module release and versioning workflow](https://go.dev/doc/modules/release-workflow)
