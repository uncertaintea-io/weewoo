# Release Procedures

## Container Registry Access

We use the GitHub Container Registry (ghcr.io) to store our images.
In order to access the registry, you will need a GitHub Personal Access Token; specifically a "Personal Access Token (classic)".
GutHub's instructions are [here](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

I recommend storing this token in a `secrets/docker.token` file. If you do, lock the file down so only you can read it:

```bash
chmod 400 secrets/docker.token
```

You can then use the token to login to the container registry:

```bash
cat secrets/docker.token | docker login ghcr.io -u ${GITHUB_USER} --password-stdin
```

## Docker buildx

In order to build the multi-platform images we need, you will need to have Docker's `buildx` extension installed and configured.
You can see if you already have it by running:

```shell
docker buildx version
docker buildx ls
```

When you run that last command, you should see a "multiarch" configuration.
If you don't, you can set it up as follows:

```shell
make ui-build # temporarily needed, see issue #57
docker buildx create --name multiarch --driver docker-container --use
docker buildx inspect --bootstrap
```

## Building the Docker Image

Run this in the root directory of the repo:

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
