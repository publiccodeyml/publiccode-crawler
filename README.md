# publiccode.yml crawler

[![Go Report Card](https://goreportcard.com/badge/github.com/italia/publiccode-crawler/v4)](https://goreportcard.com/report/github.com/italia/publiccode-crawler/v4)

## Description

`publiccode-crawler` retrieves the `publiccode.yml` files from the
repositories of the publishers listed in an
[open-catalog-api](https://github.com/publiccodeyml/open-catalog-api) instance.

## Setup and deployment processes

`publiccode-crawler` can either run manually on the target machine or it can be deployed
from a Docker container.

### Manually configure and build

1. Rename `config.toml.example` to `config.toml` and set the variables

   > **NOTE**: The application also supports environment variables in substitution
   > to config.toml file. Remember: "environment variables get higher priority than
   > the ones in configuration file"

2. Build the binary with `go build`

3. Set the credentials before running a crawl:

   ```console
   export GITHUB_TOKEN="<GitHub API token>"
   export API_BEARER_TOKEN="<open-catalog-api bearer token>"
   ./publiccode-crawler crawl
   ```

   `GITHUB_TOKEN` is required only to crawl sources hosted on GitHub. Without
   it, GitHub sources fail and the other sources are still crawled.
   `API_BEARER_TOKEN` authorizes the requests that save crawl results to
   open-catalog-api.
   Environment variables take precedence over values in `config.toml`.

### Docker

You can build the Docker image using

```console
docker build .
```

or use the image published to DockerHub. Set the same credentials in the host
environment and pass them to the container:

```console
export GITHUB_TOKEN="<GitHub API token>"
export API_BEARER_TOKEN="<open-catalog-api bearer token>"
docker run --rm \
  -e GITHUB_TOKEN \
  -e API_BEARER_TOKEN \
  publiccodeyml/publiccode-crawler
```

### Prometheus metrics

While a crawl is running, Prometheus metrics are available at
`http://<crawler-host>:8081/metrics`. Scrape this endpoint to record the
outcome of each run.

The crawler exposes these counters:

* `publiccode_crawler__repository_processed`: repositories processed.
* `publiccode_crawler__repository_good_publiccodeyml`: valid
  `publiccode.yml` files found.
* `publiccode_crawler__repository_bad_publiccodeyml`: invalid
  `publiccode.yml` files found.
* `publiccode_crawler__repository_cloned`: repositories cloned for
  vitality calculations.
* `publiccode_crawler__repository_new`: new repositories saved to the API.
* `publiccode_crawler__repository_known`: existing repositories processed.
* `publiccode_crawler__repository_fetch_failed`: failed HTTP downloads of
  `publiccode.yml` other than 404 responses.
* `publiccode_crawler__repository_upsert_failures`: failures while
  creating or updating software in the API.

## Commands

### `publiccode-crawler crawl`

Gets the list of publishers from `https://api.developers.italia.it/v1/publishers`
and starts to crawl their repositories.

### `publiccode-crawler crawl publishers*.yml`

Gets the list of publishers in `publishers*.yml` and starts to crawl
their repositories.

### `publiccode-crawler crawl-software <software> <publisher>`

Crawl just the software specified as parameter.
It takes the software URL and its publisher id as parameters.

Ex. `publiccode-crawler crawl-software https://api.developers.italia.it/v1/software/a2ea59b0-87cd-4419-b93f-00bed8a7b859 edb66b3d-3e36-4b69-aba9-b7c4661b3fdd`

### Other commands

* `crawler download-publishers` downloads organizations and repositories from
  the [onboarding portal repository](https://github.com/italia/developers-italia-onboarding)
  and saves them to a publishers YAML file.

## See also

* [open-catalog-api](https://github.com/publiccodeyml/open-catalog-api): the API
  used to store the results of the crawling
* [libpubliccode](https://github.com/publiccodeyml/libpubliccode): the Go
  package for parsing publiccode.yml files

## Authors

The version control system provides attribution for specific lines of code.
