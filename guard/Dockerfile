# The injection screen (docs/design/sentry.md, section 6): CPU torch and the
# model baked in at a pinned revision, so the container needs no network.
#
#   docker build -t eika-guard guard
#   docker build --build-arg TORCH_INDEX=https://download.pytorch.org/whl/cu128 guard   # GPU
FROM python:3.12-slim-bookworm

ARG TORCH_INDEX=https://download.pytorch.org/whl/cpu
ARG MODEL=Horizon-Labs/prompt-injection-guard-small
ARG REVISION=1b7c40f4a5fdbf9622d20372ead279e65d4c7fc9

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir --extra-index-url "$TORCH_INDEX" -r requirements.txt

# Only the safetensors weights and the config and tokenizer files are needed.
RUN python -I -c "from huggingface_hub import snapshot_download; \
snapshot_download('$MODEL', revision='$REVISION', local_dir='/model', \
allow_patterns=['*.json', 'model.safetensors'])" && \
    echo "$MODEL@$REVISION" > /model/NAME

COPY guard.py test_guard.py ./
RUN python -m unittest -v test_guard

ENV HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1
USER nobody
EXPOSE 9940
HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=12 \
  CMD ["python", "-I", "-c", "import urllib.request as u; u.urlopen('http://127.0.0.1:9940/health', timeout=2)"]
CMD ["python", "-I", "guard.py", "--model", "/model", "--host", "0.0.0.0", "--port", "9940"]
