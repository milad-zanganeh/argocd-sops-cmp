# argocd-sops-cmp

`argocd-sops-cmp` is an Argo CD
[Config Management Plugin (CMP)](https://argo-cd.readthedocs.io/en/stable/user-guide/config-management-plugins/) that
renders a Helm chart and its SOPS-encrypted secrets together in a single pass.

Argo CD renders Helm natively but has no built-in way to handle SOPS-encrypted secrets kept in a repository; the common
workarounds add a second tool, a separate Application, or a mutating controller. This plugin combines both steps into
one: it runs `helm template` on the chart — honouring values files, `--set` parameters, CRDs, and the release name and
namespace — and appends every file under `secrets/`, decrypted with [SOPS](https://github.com/getsops/sops) and GnuPG, to
the same manifest stream.

Because the rendered manifests and the decrypted `Secret` manifests are emitted together, Argo CD manages the secrets
like any other resource, so diffing, pruning, and self-heal all behave as expected. The repository only ever stores
ciphertext: decryption happens in memory inside the repo-server sidecar, and the plugin never runs `kubectl apply`.

## Quick start

1. **Lay out your app** with a chart, a `secrets/` directory, or both:

   ```
   my-app/
   ├── Chart.yaml            # optional — omit for a secrets-only app
   ├── values.yaml
   ├── templates/
   └── secrets/              # optional — omit for a chart-only app
       ├── db-credentials.yaml   # SOPS-encrypted
       └── tls.yaml              # SOPS-encrypted
   ```

2. **Encrypt your secrets** with SOPS so only ciphertext is committed:

   ```bash
   sops --encrypt --in-place my-app/secrets/db-credentials.yaml
   ```

3. **Point an Application at it** and name the plugin:

   ```yaml
   apiVersion: argoproj.io/v1alpha1
   kind: Application
   spec:
     source:
       path: my-app
       plugin:
         name: argocd-sops-cmp
         env:
           - name: HELM_VALUE_FILES        # comma-separated, repo-relative
             value: "values.yaml,values-prod.yaml"
           - name: HELM_PARAMETERS         # passed to helm --set
             value: "image.tag=1.4.2"
           - name: HELM_RELEASE_NAME       # optional; defaults to the app name
             value: "my-app"
   ```

Argo CD passes those env vars to the plugin as `ARGOCD_ENV_*`. Every `HELM_VALUE_FILES` path must exist and stay inside
the app source directory — absolute paths and `../` traversal out of the repo are rejected.

## How it works

`argocd-sops-cmp` implements the three CMP phases Argo CD calls in turn:

- **discover** claims the directory whenever it finds a `Chart.yaml` or a `secrets/` directory, so an app can be
  chart-only, secrets-only, or both.
- **init** resolves chart dependencies before rendering — `helm dependency build` when a `Chart.lock` is present,
  otherwise `update`. It adds any classic `http(s)` dependency repos, injecting credentials only for your configured
  private repo host, and does nothing for secrets-only apps or charts that already vendor their `charts/`.
- **generate** runs `helm template` (when a chart exists) and then appends the decrypted `secrets/*.yaml` and
  `secrets/*.yml` files, sorted so the output is byte-stable across renders.

## Installing into Argo CD

Build and push the sidecar image yourself (see the [`Dockerfile`](Dockerfile)), then wire it into the
[argo-cd Helm chart](https://github.com/argoproj/helm-charts) values. Only the plugin-related keys are shown below;
merge them into your existing values. Replace the `<...>` placeholders with your own registry and Helm repo.

```yaml
configs:
  # Register the plugin. The chart renders this into the argocd-cmp-cm
  # ConfigMap (key argocd-sops-cmp.yaml). It mirrors plugin.yaml in this repo.
  cmp:
    create: true
    plugins:
      argocd-sops-cmp:
        version: v1.0
        discover:
          find:
            command: [argocd-sops-cmp, discover]
        init:
          command: [argocd-sops-cmp, init]
        generate:
          command: [argocd-sops-cmp, generate]

repoServer:
  # Only needed if your registry is private.
  imagePullSecrets:
    - name: <your-registry-pull-secret>

  automountServiceAccountToken: false

  extraContainers:
    - name: argocd-sops-cmp
      image: <your-registry>/argocd-sops-cmp:<tag>
      imagePullPolicy: IfNotPresent
      command:
        - /var/run/argocd/argocd-cmp-server
      env:
        - name: SOPS_GPG_KEYGRIP
          valueFrom:
            secretKeyRef:
              name: argocd-sops-gpg
              key: keygrip
        - name: SOPS_GPG_PASSPHRASE
          valueFrom:
            secretKeyRef:
              name: argocd-sops-gpg
              key: passphrase
        - name: SOPS_GPG_KEY_FILE
          value: /argocd-sops-cmp/keys/private-key.asc
        - name: HELM_REPO_HOST
          value: <your-helm-repo-host>
        - name: HELM_REPO_USERNAME
          valueFrom:
            secretKeyRef:
              name: <your-helm-repo-secret>
              key: username
        - name: HELM_REPO_PASSWORD
          valueFrom:
            secretKeyRef:
              name: <your-helm-repo-secret>
              key: password
      securityContext:
        runAsNonRoot: true
        runAsUser: 999
        runAsGroup: 999
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        seccompProfile:
          type: RuntimeDefault
        capabilities:
          drop:
            - ALL
      resources:
        requests:
          cpu: 50m
          memory: 128Mi
        limits:
          cpu: 500m
          memory: 512Mi
      volumeMounts:
        - name: var-files
          mountPath: /var/run/argocd
        - name: plugins
          mountPath: /home/argocd/cmp-server/plugins
        - name: argocd-cmp-cm
          mountPath: /home/argocd/cmp-server/config/plugin.yaml
          subPath: argocd-sops-cmp.yaml
        - name: cmp-tmp
          mountPath: /tmp
        - name: sops-gpg-key
          mountPath: /argocd-sops-cmp/keys
          readOnly: true

  volumes:
    - name: argocd-cmp-cm
      configMap:
        name: argocd-cmp-cm
    - name: cmp-tmp
      emptyDir: {}
    - name: sops-gpg-key
      secret:
        secretName: argocd-sops-gpg
        items:
          - key: private-key.asc
            path: private-key.asc
```

You also need a Secret named `argocd-sops-gpg` holding your GPG key material — the keys it expects
(`private-key.asc`, `passphrase`, `keygrip`) are described under [Configuration](#configuration).

## Configuration

The plugin reads a few environment variables from the sidecar container (not per-Application). For decryption it needs
`SOPS_GPG_PASSPHRASE` (the GPG key passphrase) and `SOPS_GPG_KEYGRIP` (the keygrip `gpg-preset-passphrase` presets it
for); the armored private key is read from `SOPS_GPG_KEY_FILE`, defaulting to `/argocd-sops-cmp/keys/private-key.asc`. If your
chart pulls dependencies from a private Helm repo, set `HELM_REPO_HOST`, `HELM_REPO_USERNAME` and `HELM_REPO_PASSWORD` —
the credentials are applied only to dependency repos on that host.

Decryption runs in a private, ephemeral `GNUPGHOME` created per invocation; the passphrase is fed to
`gpg-preset-passphrase` on stdin (never on the command line), and the throwaway `GNUPGHOME` and its `gpg-agent` are torn
down when the process exits.

## Developing

It's plain Go with a single dependency (`gopkg.in/yaml.v3`):

```bash
go test ./...
go vet ./...
go build .      # or let the Dockerfile's build stage compile it
```

## License

Licensed under the [Apache License 2.0](LICENSE).
