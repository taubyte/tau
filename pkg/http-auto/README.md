# HTTP Auto Package

The `http-auto` package is a part of the Taubyte project, designed to facilitate automatic HTTP service management with features such as domain validation, TLS support, and caching.

## Features

- **Automatic Domain Validation**: Validates service or alias domains using TLS client hello information.
- **TLS Support**: Integrates with ACME for certificate management.
- **Caching**: Implements positive and negative caching for domain validation results.

## Using a Custom ACME Server (StepCA)

### Install StepCA

If you already have StepCA installed, skip this step. Otherwise, follow this simple guide to run a local instance for testing.

1. Pull the StepCA Docker image:

    ```bash
    docker pull smallstep/step-ca
    ```

2. Run the StepCA container:

    ```bash
    docker run -d --name step-ca \
      -v step:/home/step \
      -p 9000:9000 \
      -e DOCKER_STEPCA_INIT_NAME=StepCA \
      -e DOCKER_STEPCA_INIT_DNS_NAMES=localhost \
      smallstep/step-ca
    ```

3. Ensure it's running:

    ```bash
    curl http://localhost:9000/health
    ```

4. Connect to the container:

    ```bash
    docker exec -it step-ca sh
    ```

5. Add an ACME provider:

    ```bash
    step ca provisioner add acme --type ACME
    ```

6. Restart the container:

    ```bash
    docker restart step-ca
    ```

### Configure Tau

Edit the configuration file for your setup and add the following:

```yaml
domains:
    acme:
        url: https://localhost:9000/acme/acme/directory
        ca:
            skip: true
```

If you want to add a RootCA, use:

```yaml
domains:
    acme:
        url: https://localhost:9000/acme/acme/directory
        ca:
            root-ca: "/path/to/root/ca/file"
```
