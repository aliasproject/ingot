# Scale to zero

Projects that nobody is using are stopped, and the next request to one starts
it again. Only the projects you're actually working on use CPU and memory, so
you can keep many sites set up without running them all.

```
alios idle            # show this project's setting
alios idle off        # never stop it
alios idle 2h         # stop it after 2h idle instead of the default
alios idle on         # back to the default (ALIOS_IDLE_TIMEOUT)
```

The default is 30 minutes. To change it, set `ALIOS_IDLE_TIMEOUT` (for
example `1h`, or `off`) and run `alios global restart`. The service keeps that
value until a later global start runs with the variable set to something
else, so a terminal without it never resets the timeout. `alios status`,
`alios idle` and `alios doctor` report the value the service is actually
running with. `alios idle` stores a project's own setting in a container
label, the same way `alios env` does, so changing it recreates the container.

## How it works

A global service, `alios_idle`, runs next to Caddy and does two jobs.

**Stopping.** Every 30 seconds it reads each running project's counters from
the Docker API and treats the project as active if any of these is true:

- **Network:** more than about 8 KiB/min of traffic. Any page view, API call
  or open dev-server connection counts, and so does an app talking to MySQL.
- **CPU:** more than 1% of a core. An idle FrankenPHP container uses about
  0.1%.
- **Exec sessions:** any process that was exec'd into the container rather
  than started by its server, seen in two samples in a row. That covers
  `alios exec` and `alios shell` for as long as they're open, including an
  idle shell, which uses no CPU or traffic. A health check that happens to be
  running when a sample is taken doesn't count. Processes the server itself
  starts, such as its workers or a supervisor's queue workers, aren't exec
  sessions, so only their CPU and traffic count.

A project that stays quiet on all three for the timeout is stopped. Docker
remembers that it was stopped, so it stays stopped after a reboot, and only
the projects you were using come back.

**Waking.** A project's vhost ends with `import alios_wake <project>`. When the
container is stopped, Caddy can't reach it, and the request goes to
`alios_idle` instead. It starts the container, waits until it accepts
connections (usually under a second), then replies with a `307` redirect to
the same URL so the browser tries again. With a `307` the browser resends the
original method and body, so a form POST or an API call is resubmitted, not
turned into a GET. The waker itself only ever sees a bodiless GET. Only
Caddy's own upstream failures (502, 503, 504) go to the waker, so an app's
error pages and a share's basic-auth prompt pass through untouched.

`alios_idle` is optional. It runs outside the compose group, so if its image
can't be pulled, `alios global start` warns and carries on. DNS, Caddy and
your projects still work, and nothing is stopped or woken. A stopped project
then shows Caddy's empty 502 until `alios_idle` is running again, and
`alios doctor` reports it. `alios pause` stops it before the projects, so a
request arriving mid-pause can't start one again.

`alios exec`, `alios shell` and `alios share` start a sleeping project
themselves. `alios list` and `alios status` show a stopped project as
"sleeping" while `alios_idle` is running, because a visit starts it.
`alios stop` therefore works like putting a project to sleep: visiting it
starts it again.

## Busy without traffic

A project that keeps using CPU for 2 hours with no traffic at all is either
running a long job or stuck. (An open but idle `alios shell` keeps a project
awake without counting as busy.) `alios_idle` keeps it running
either way, and logs a `busy-warning` line, which `alios doctor` shows. Check
it with `alios logs` or `docker top alios_php_<project>`.

## When to turn it off

Use `alios idle off` for a project that has to keep running with no requests:
one with a queue worker or scheduler running inside its container, for
example. A worker the container's server starts isn't an exec session, so
only its CPU and traffic count, and its polling may be too light to register,
so the project can be stopped while the worker waits for jobs.

## Upgrading

Vhosts written by an older alios don't have the `import alios_wake` line, so
visiting one of those projects while it's stopped shows Caddy's error instead
of starting it. Each vhost is rewritten on that project's next `alios start`,
or run `alios cert repair` to update them all; it writes the base Caddyfile
with the snippet first. `alios global start` and `alios doctor` point out any
that are left. A shared project's share vhost picks up the snippet the next
time it's shared.

## Security

`alios_idle` mounts the Docker socket so that it can read stats and start and
stop containers. Mounting the socket gives a container root-equivalent access
to the host. The service only ever starts a container named
`alios_php_<project>` that carries the matching `alios.project.name` label.
Its waker listens only on the internal `alios_internal` network, with no port
published to the host. Rootless Docker isn't supported yet: the socket path is
fixed at `/var/run/docker.sock`, which Docker Desktop also provides on macOS
and Windows.
