# Parallel Artifact Downloader - Buildkite Plugin

Downloads buildkite artifacts in parallel (much faster)

## Example

Add the following to your `pipeline.yml`:

```yml
steps:
  - command: ls
    plugins:
      - dgodd/parallel-artifact#v1.0.0:
          pattern: '*.md'
```

## Configuration

### `pattern` (Required, string)

The file name pattern to download, for example `*.ts`.

### Development

```
docker-compose run --rm lint
```

To build (before committing the file):
```
go build -o bin/parallel-artifact .
```
