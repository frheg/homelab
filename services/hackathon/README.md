# hackathon

The runtime for the hackathon pod (`apps/hackathon`): Python 3.13 with uv, Node 22, git and curl. It runs
`/srv/hackathon/app/run.sh` from kratos's disk, and again 3 s after it stops, so code changes need no new image.
Until there is a run.sh it shows a page that says what to do.

Language: shell and Python, because the image is a runtime for whatever the app is written in (no Go here).

`starter/` is a FastAPI app that checks every connection the pod has (llm, embeddings, Postgres, Qdrant, SearXNG,
internet, data folder) and asks the model. Copy it to `/srv/hackathon/app` and replace it.

New image (only when the runtime changes, not for app changes):

    docker build -t kratos:5000/hackathon:<version> services/hackathon
    skopeo --insecure-policy copy --dest-tls-verify=false docker-daemon:kratos:5000/hackathon:<version> docker://kratos:5000/hackathon:<version>
    skopeo inspect --tls-verify=false --format '{{.Digest}}' docker://kratos:5000/hackathon:<version>

then pin the digest in `apps/hackathon/hackathon.yaml`.
