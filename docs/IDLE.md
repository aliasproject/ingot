# Scale to zero

Projects that nobody is using are stopped, and the next request to one starts
it again. Only the projects you're actually working on use CPU and memory, so
you can keep many sites set up without running them all.

```
ingot idle            # show this project's setting
ingot idle off        # never stop it
ingot idle 2h         # stop it after 2h idle instead of the default
ingot idle on         # back to the default (INGOT_IDLE_TIMEOUT)
```

The default is 30 minutes. To change it, set `INGOT_IDLE_TIMEOUT` (for
example `1h`, or `off`) and run `ingot global restart`. The service keeps that
value until a later global start runs with the variable set to something
else, so a terminal without it never resets the timeout. `ingot status`,
`ingot idle` and `ingot doctor` report the value the service is actually
running with. `ingot idle` stores a project's own setting in a container
label, the same way `ingot env` does, so changing it recreates the container.

## How it works

A global service, `ingot_idle`, runs next to Caddy and does two jobs.

**Stopping.** Every 30 seconds it reads each running project's counters from
the Docker API and treats the project as active if any of these is true:

- **Network:** more than about 8 KiB/min of traffic. Any page view, API call
  or open dev-server connection counts, and so does an app talking to MySQL.
- **CPU:** more than 1% of a core. An idle FrankenPHP container uses about
  0.1%.
- **Exec sessions:** any process that was exec'd into the container rather
  than started by its server, seen in two samples in a row. That covers
  `ingot exec` and `ingot shell` for as long as they're open, including an
  idle shell, which uses no CPU or traffic. A health check that happens to be
  running when a sample is taken doesn't count. Processes the server itself
  starts, such as its workers or a supervisor's queue workers, aren't exec
  sessions, so only their CPU and traffic count.

A project that stays quiet on all three for the timeout is stopped. Docker
remembers that it was stopped, so it stays stopped after a reboot, and only
the projects you were using come back.

**Waking.** A project's vhost ends with `import ingot_wake <project>`. When the
container is stopped, Caddy can't reach it, and the request goes to
`ingot_idle` instead. It starts the container, waits until it accepts
connections (usually under a second), then replies with a `307` redirect to
the same URL so the browser tries again. With a `307` the browser resends the
original method and body, so a form POST or an API call is resubmitted, not
turned into a GET. The waker itself only ever sees a bodiless GET. Only
Caddy's own upstream failures (502, 503, 504) go to the waker, so an app's
error pages and a share's basic-auth prompt pass through untouched.

`ingot_idle` is optional. It runs outside the compose group, so if its image
can't be pulled, `ingot global start` warns and carries on. DNS, Caddy and
your projects still work, and nothing is stopped or woken. A stopped project
then shows Caddy's empty 502 until `ingot_idle` is running again, and
`ingot doctor` reports it. `ingot pause` stops it before the projects, so a
request arriving mid-pause can't start one again.

`ingot exec`, `ingot shell` and `ingot share` start a sleeping project
themselves. `ingot list` and `ingot status` show such a project as
"sleeping".

## Sleeping vs stopped

A project the watcher stopped for being idle is **sleeping**: the next visit
starts it. A project you stop with `ingot stop` is **stopped**: it stays down
until `ingot start`. A visit gets a short "stopped" page instead, and `exec`,
`shell` and `share` ask you to start it first. That way, a browser tab that
keeps polling the project can't undo the stop.

The difference is recorded in the project's vhost: `ingot stop` swaps its
`import ingot_wake <project>` line for `import ingot_stopped <project>`, and
`ingot start` swaps it back, and so do the other commands that start the
container (`restart`, `rebuild`, `php`, `image`). `ingot pause`/`resume`,
`ingot cert repair` and `ingot cert remove` keep it.

## Busy without traffic

A project that keeps using CPU for 2 hours with no traffic at all is either
running a long job or stuck. (An open but idle `ingot shell` keeps a project
awake without counting as busy.) `ingot_idle` keeps it running
either way, and logs a `busy-warning` line, which `ingot doctor` shows. Check
it with `ingot logs` or `docker top ingot_php_<project>`.

## When to turn it off

Use `ingot idle off` for a project that has to keep running with no requests:
one with a queue worker or scheduler running inside its container, for
example. A worker the container's server starts isn't an exec session, so
only its CPU and traffic count, and its polling may be too light to register,
so the project can be stopped while the worker waits for jobs.

## Upgrading

Vhosts written by an older ingot don't have the `import ingot_wake` line, so
visiting one of those projects while it's stopped shows Caddy's error instead
of starting it. Each vhost is rewritten on that project's next `ingot start`,
or run `ingot cert repair` to update them all; it writes the base Caddyfile
with the snippet first. `ingot global start` and `ingot doctor` point out any
that are left. A shared project's share vhost picks up the snippet the next
time it's shared.

## Security

`ingot_idle` mounts the Docker socket so that it can read stats and start and
stop containers. Mounting the socket gives a container root-equivalent access
to the host. The service only ever starts a container named
`ingot_php_<project>` that carries the matching `ingot.project.name` label.
Its waker listens only on the internal `ingot_internal` network, with no port
published to the host. Rootless Docker isn't supported yet: the socket path is
fixed at `/var/run/docker.sock`, which Docker Desktop also provides on macOS
and Windows.
