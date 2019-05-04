# Lok8
> Pronounced as `locate` derived from `loki` and `k8s`

### :construction:  This is currently WIP but should work.

Lok8 is adapter allowing you to view logs of containers running in [Kubernetes](https://kubernetes.io/) using [Grafna](https://grafana.com/) Explore mode same way
you do with `kubectl logs`.

# Motivation
Main motivation was to make use of logging integration in Grafana _(which is great
in combination with Prometheus metrics, thank you guys!)_ but without the overhead of running some daemonset,
database, streaming architecture and so on. This single binary gives you access
to all the recent logs from your containers. _(Nothing against Loki, it's probably
easy to run but there will be always the operational overhead to monitor the whole streaming architecture etc)_

# Tradeoffs
The main downside is that **all the data goes throught the Kubernetes API server**.
You need to be aware of this and keep it in mind when querying for logs.
The second downside is that **you have only that much of a log history which you
can get using the `kubectl logs` plus `kubectl logs -p`.**

# Building it
```bash
cd cmd/lok8
go build
```

# Running it
```bash
kubectl config view -o yaml > k8s_confg.yaml
./lok8 -c k8s_confg.yaml
```

It will automatically start to watch for all pods in all namespaces and expose API on `http://0.0.0.0:3001`.
If you want to watch just some namespaces use the repeated `-n` flag.
```bash
./lok8 -c k8s_confg.yaml -n my-namespace -n my next-namespace
```

See `lok8 --help` for more info.

# Using in grafana
> Note that you need Grafana version 6+ which has the Explore mode

Go to your Grafana and add new datasource of type `Loki`.
Set the URL to to point to the Lok8 instance e.g `http://0.0.0.0:3001`.

That's it go to the Explore mode, select the datasource and you should be abble to query your logs.

# TODO
- [ ] Swicth to straming architecture.
- [ ] Make log lines limit exact.
- [ ] Add Dockerfile and k8s deployment manifests.
- [ ] Add metrics.
- [ ] Possibly add cache to mitigate impact of repeated queries.
