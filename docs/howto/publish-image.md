# Publishing the cloud image

The server image is built by GitHub Actions and pushed to Docker Hub
(`.github/workflows/server-image.yml`). The VPS then pulls a tag instead of
receiving an image over SSH from someone's laptop.

## One-time setup

1. **Docker Hub token** — Account Settings → Personal access tokens → new token,
   Read & Write. Use a token rather than the account password: it is scoped to
   registry access and can be revoked on its own.
2. **GitHub** — repo → Settings → Secrets and variables → Actions:

   | Kind     | Name                 | Value                        |
   |----------|----------------------|------------------------------|
   | Secret   | `DOCKERHUB_TOKEN`    | the token from step 1        |
   | Variable | `DOCKERHUB_USERNAME` | Docker Hub account/org name  |

3. **Repository on Docker Hub** — create `<username>/alimdm`. **This repo
   is private, so make the Docker Hub repository private too** unless you intend
   to publish the built server and console. The image holds no secrets — they
   all arrive as environment variables — but it is still this codebase compiled.
   Docker Hub's free plan includes one private repository.

## What gets published

| Trigger                | Tags                                          |
|------------------------|-----------------------------------------------|
| push to `main`         | `latest`, `sha-<commit>`                      |
| push of tag `v1.4.0`   | `1.4.0`, `1.4`, `sha-<commit>`                |
| manual run             | as for the branch or tag it runs on           |

Deploy by digest or a `sha-` tag rather than `latest` when you want to know
exactly what is running: `latest` moves under you on the next merge.

## Deploying a published image

On the VPS, replacing the `docker save | ssh docker load` pipeline:

```bash
docker login -u <username>          # once, if the repository is private
docker pull <username>/alimdm:latest
docker stop alimdm-cloud && docker rm alimdm-cloud
docker run -d --name alimdm-cloud --restart unless-stopped \
  --env-file /home/tech/ali-mdm/alimdm.env \
  -v /home/tech/ali-mdm/data:/data -p 127.0.0.1:8080:8080 \
  <username>/alimdm:latest
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/healthz
```

`docker restart` does **not** re-read `--env-file`. After changing
`alimdm.env`, the container has to be recreated as above or it keeps running on
the environment it started with.

## GHCR instead

For a private repository, GitHub's own registry is usually the better fit: no
second account, unlimited private packages, and the workflow authenticates with
the built-in `GITHUB_TOKEN` rather than a stored credential. Swap the login and
image lines:

```yaml
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository }}/alimdm-cloud
```

and add `packages: write` to the job's `permissions`. The VPS then authenticates
to `ghcr.io` with a personal access token that has `read:packages`.
