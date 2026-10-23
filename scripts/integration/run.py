# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///

import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path
import queue
import re
import secrets
import select
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time


PIN = "1234"
WRONG_PIN = "9999"
MAX_LINE = 64 << 10
PROFILES = ("modern", "roku", "lg", "appletv3", "uxplay", "airserver")
LEGACY_DECRYPTION_PROFILES = {"appletv3", "uxplay"}
RECEIVER_ERROR_COUNTERS = ("video_crypto_errors", "video_malformed")


class Failure(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise Failure(message)


def isolated_network(interfaces):
    if sum(item.get("ifname") == "lo" for item in interfaces) != 1:
        return False
    for item in interfaces:
        if item.get("ifname") == "lo":
            if item.get("link_type") != "loopback":
                return False
            continue
        kind = item.get("linkinfo", {}).get("info_kind")
        data = item.get("linkinfo", {}).get("info_data", {})
        if not (
            kind in ("gre", "gretap", "erspan")
            and item.get("ifname") == kind + "0"
            and item.get("operstate") == "DOWN"
            and "UP" not in item.get("flags", [])
            and not item.get("addr_info")
            and not item.get("link")
            and not item.get("master")
            and data.get("local") == "any"
            and data.get("remote") == "any"
        ):
            return False
    return True


def redact(text):
    return re.sub(r"(?<!\d)(?:1234|9999)(?!\d)", "[dummy credential redacted]", text)


def parse_receiver_stats(line):
    video = re.search(r"\bvideo=(\d+)/(\d+)B\b", line)
    if not video:
        return None
    stats = {"video_packets": int(video[1]), "video_bytes": int(video[2])}
    aliases = {
        "video_decrypted": ("video_decrypted", "decrypted"),
        "video_crypto_errors": ("video_crypto_errors", "crypto_errors"),
        "video_malformed": ("video_malformed", "malformed"),
        "video_frames": ("video_frames",),
    }
    for name, keys in aliases.items():
        match = re.search(r"\b(?:" + "|".join(keys) + r")=(\d+)\b", line)
        if match:
            stats[name] = int(match[1])
    return stats


def validate_receiver_errors(stats):
    for key in RECEIVER_ERROR_COUNTERS:
        require(stats.get(key, 0) == 0, f"receiver reported {key}")


def validate_transport(frames, payload_bytes, stats, baseline, profile, strict):
    require(
        frames > 0 and payload_bytes > 0,
        "no positive transmitted frame/payload counters",
    )
    for key in ("video_packets", "video_bytes"):
        require(
            stats.get(key, 0) > baseline.get(key, 0), f"receiver {key} did not increase"
        )
    validate_receiver_errors(stats)
    decryption = "not implemented for this receiver profile"
    if profile in LEGACY_DECRYPTION_PROFILES:
        decryption = "not exposed by receiver CLI"
        if "video_decrypted" in stats:
            require(
                stats["video_decrypted"] > baseline.get("video_decrypted", 0),
                "receiver decrypted no new legacy video packets",
            )
            decryption = "positive legacy decrypted-packet delta; AVCC validation, not picture decode"
        elif strict:
            raise Failure(
                "legacy decryption counters are required but absent from receiver CLI"
            )
    elif stats.get("video_decrypted", 0) > baseline.get("video_decrypted", 0):
        decryption = "positive decrypted-packet delta; not picture decode"
    return {
        "transmitted_frames": frames,
        "transmitted_payload_bytes": payload_bytes,
        "receiver_before": baseline,
        "receiver_after": stats,
        "decryption_check": decryption,
    }


def png_dimensions(path):
    with path.open("rb") as source:
        header = source.read(24)
    require(
        len(header) == 24
        and header[:8] == b"\x89PNG\r\n\x1a\n"
        and header[12:16] == b"IHDR",
        "desktop image must be a PNG fixture",
    )
    width, height = struct.unpack(">II", header[16:24])
    require(
        640 <= width <= 3840 and 360 <= height <= 2160,
        "desktop PNG must be between 640x360 and 3840x2160",
    )
    require(width % 2 == 0 and height % 2 == 0, "desktop PNG dimensions must be even")
    return width, height


def parse_desktop_psnr(text):
    match = re.search(
        r"(?m)^n:1\s+[^\r\n]*\bpsnr_avg:([0-9]+(?:\.[0-9]+)?|inf)(?:\s|$)", text
    )
    require(match is not None, "desktop comparison produced no RGB PSNR")
    return match[1]


class Child:
    def __init__(self, name, args, env, directory):
        self.name = name
        self.failure = None
        self.lines = queue.Queue(maxsize=2048)
        self.record = {
            "name": name,
            "args": [str(arg) for arg in args],
            "forced_kill": False,
        }
        self.process = subprocess.Popen(
            args,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            start_new_session=True,
            bufsize=0,
        )
        self.threads = []
        for stream, pipe in (
            ("stdout", self.process.stdout),
            ("stderr", self.process.stderr),
        ):
            thread = threading.Thread(
                target=self._read,
                args=(stream, pipe, directory / f"{name}.{stream}.log"),
                daemon=True,
            )
            thread.start()
            self.threads.append(thread)

    def _read(self, stream, pipe, path):
        written = 0
        try:
            with path.open("x", encoding="utf-8") as log:
                while True:
                    line = pipe.readline(MAX_LINE + 2)
                    if not line:
                        break
                    if len(line.rstrip(b"\r\n")) > MAX_LINE:
                        self.failure = (
                            f"{self.name} {stream} exceeded the 64 KiB line limit"
                        )
                        break
                    if written < 2 << 20:
                        text = redact(line.decode("utf-8", errors="replace"))
                        text = text.encode("utf-8")[: (2 << 20) - written].decode(
                            "utf-8", errors="ignore"
                        )
                        log.write(text)
                        log.flush()
                        written += len(text.encode("utf-8"))
                    if stream == "stdout":
                        self.lines.put_nowait((time.monotonic(), line))
        except (OSError, queue.Full) as error:
            self.failure = f"{self.name} output reader failed: {type(error).__name__}"
        finally:
            if stream == "stdout":
                with contextlib.suppress(queue.Full):
                    self.lines.put_nowait((time.monotonic(), None))

    def next_line(self, deadline, allow_eof=False):
        require(self.failure is None, self.failure)
        remaining = deadline - time.monotonic()
        require(remaining > 0, f"timed out waiting for {self.name}")
        try:
            stamp, line = self.lines.get(timeout=remaining)
        except queue.Empty:
            raise Failure(f"timed out waiting for {self.name}") from None
        require(self.failure is None, self.failure)
        if line is None and allow_eof:
            return stamp, None
        require(
            line is not None,
            f"{self.name} stdout closed unexpectedly, exit={self.process.poll()}",
        )
        return stamp, line.decode("utf-8", errors="strict").rstrip("\r\n")

    def write(self, value, timeout=8):
        data = memoryview(value)
        deadline = time.monotonic() + timeout
        fd = self.process.stdin.fileno()
        os.set_blocking(fd, False)
        while data:
            remaining = deadline - time.monotonic()
            require(remaining > 0, f"{self.name} stdin write timed out")
            _, writable, _ = select.select([], [fd], [], remaining)
            require(writable, f"{self.name} stdin write timed out")
            try:
                data = data[os.write(fd, data) :]
            except BlockingIOError:
                continue
            except BrokenPipeError:
                raise Failure(f"{self.name} input closed unexpectedly") from None

    def send(self, value):
        self.write(json.dumps(value, separators=(",", ":")).encode() + b"\n")

    def eof(self):
        if not self.process.stdin.closed:
            self.process.stdin.close()

    def wait(self, timeout=8, expected_exit=0):
        try:
            code = self.process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            raise Failure(f"{self.name} did not exit within {timeout}s") from None
        self.record["exit_status"] = code
        self.record["expected_exit_status"] = expected_exit
        require(
            code >= 0,
            f"{self.name} terminated by signal {-code}, expected normal exit {expected_exit}",
        )
        require(
            code == expected_exit,
            f"{self.name} unexpected exit status {code}, expected {expected_exit}",
        )
        require(self.failure is None, self.failure)
        return code

    def terminate(self):
        if self.process.poll() is None:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.record["forced_kill"] = True
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=3)
        self.record["exit_status"] = self.process.poll()

    def close(self):
        self.terminate()
        self.eof()
        for thread in self.threads:
            thread.join(timeout=1)
        for pipe in (self.process.stdout, self.process.stderr):
            pipe.close()


def close_children(children):
    errors = []
    for child in reversed(children):
        try:
            child.close()
        except Exception as error:
            errors.append(f"{child.name} cleanup failed: {error}")
        if child.failure:
            errors.append(child.failure)
        if child.record["forced_kill"]:
            errors.append(f"{child.name} required SIGKILL")
    return errors


class Receiver:
    def __init__(self, suite, name, profile, auth):
        self.profile = profile
        self.stats = {"video_packets": 0, "video_bytes": 0}
        self.final_stats = None
        self.sessions = []
        self.no_video = False
        self.record = {"name": name, "profile": profile, "auth": auth}
        env = dict(suite.env)
        env.pop("DOUBLETAKE_RECEIVER_CODE", None)
        if auth != "none":
            env["DOUBLETAKE_RECEIVER_CODE"] = PIN
        self.child = suite.spawn(
            name,
            [
                suite.args.receiver,
                "-listen",
                "127.0.0.1:0",
                "-profile",
                profile,
                "-auth",
                auth,
                "-name",
                "Aircast SYNTHETIC LOOPBACK acceptance fixture",
                "-stats-interval",
                "200ms",
            ],
            env,
        )
        suite.case_receivers.append(self)
        suite.case_record["receivers"].append(self.record)
        deadline = time.monotonic() + 8
        while True:
            _, line = self.child.next_line(deadline)
            match = re.search(r"listening on (127\.0\.0\.1):(\d+)\b", line)
            if match:
                self.port = int(match[2])
                break
        require(0 < self.port < 65536, "receiver returned an invalid loopback port")

    def observe(self, line):
        stats = parse_receiver_stats(line)
        if stats is not None:
            errors = {
                key: max(self.stats.get(key, 0), stats.get(key, 0))
                for key in RECEIVER_ERROR_COUNTERS
                if key in self.stats or key in stats
            }
            self.stats.update(stats)
            self.stats.update(errors)
        return stats

    def fresh_stats(self, timeout=5):
        marker = time.monotonic()
        deadline = marker + timeout
        while True:
            stamp, line = self.child.next_line(deadline)
            stats = self.observe(line)
            if stats is not None:
                if stamp >= marker:
                    validate_receiver_errors(self.stats)
                    return dict(self.stats)

    def received(self, baseline, deadline):
        while True:
            _, line = self.child.next_line(deadline)
            stats = self.observe(line)
            if stats is not None:
                validate_receiver_errors(self.stats)
                if stats["video_packets"] > baseline.get("video_packets", 0) and stats[
                    "video_bytes"
                ] > baseline.get("video_bytes", 0):
                    return dict(self.stats)

    def finish_session(self, peer, strict):
        require(
            self.sessions and self.sessions[-1]["peer"] is peer,
            "no matching receiver session to finalize",
        )
        require(
            peer.state in ("idle", "failed"),
            "receiver session finalized before sender shutdown",
        )
        require(
            peer.controller or peer.child.process.poll() is not None,
            "raw sender process is still running",
        )
        session = self.sessions[-1]
        stats = self.final_stats if self.final_stats is not None else self.fresh_stats()
        result = validate_transport(
            peer.frames,
            peer.payload_bytes,
            stats,
            session["baseline"],
            self.profile,
            strict,
        )
        session["result"].update(
            result, receiver_post_sender=dict(stats), sender_shutdown_checked=True
        )

    def shutdown(self):
        if self.final_stats is None:
            self.child.terminate()
            deadline = time.monotonic() + 8
            final_line = None
            while True:
                _, line = self.child.next_line(deadline, allow_eof=True)
                if line is None:
                    break
                stats = self.observe(line)
                if line.startswith("final stats:") and stats is not None:
                    final_line = stats
            self.final_stats = dict(self.stats)
            self.record.update(
                final_stats=self.final_stats, final_stats_line=final_line
            )
        require(
            self.record.get("final_stats_line") is not None,
            "receiver exited without final stats",
        )
        self.child.wait(expected_exit=0)
        validate_receiver_errors(self.final_stats)
        if self.no_video:
            require(
                self.final_stats["video_packets"] == 0
                and self.final_stats["video_bytes"] == 0,
                "receiver counted video after a rejected or cancelled pairing",
            )

    def verify_sessions(self, strict):
        require(
            all(
                session["result"]["sender_shutdown_checked"]
                for session in self.sessions
            ),
            "receiver session lacks a post-sender-shutdown checkpoint",
        )
        for session in self.sessions:
            result = session["result"]
            validate_transport(
                result["transmitted_frames"],
                result["transmitted_payload_bytes"],
                result["receiver_post_sender"],
                session["baseline"],
                self.profile,
                strict,
            )
            result["receiver_shutdown_checked"] = True
        if self.sessions:
            session = self.sessions[-1]
            result = session["result"]
            result.update(
                validate_transport(
                    result["transmitted_frames"],
                    result["transmitted_payload_bytes"],
                    self.final_stats,
                    session["baseline"],
                    self.profile,
                    strict,
                )
            )


class Peer:
    def __init__(self, suite, name, controller=False):
        self.suite = suite
        self.controller = controller
        self.child = suite.spawn(
            name, [suite.args.controller_probe if controller else suite.args.engine]
        )
        self.events = []
        self.reset()
        if controller:
            value = self.pump(time.monotonic() + 8)
            require(
                value.get("probe") == "ready" and value.get("ok"),
                "root controller could not find its sibling engine",
            )

    def reset(self):
        self.state = ""
        self.awaiting_start = False
        self.frames = 0
        self.payload_bytes = 0
        self.pending = None
        self.prompts = 0
        self.metadata = {}

    def accept(self, event):
        require(
            isinstance(event, dict) and isinstance(event.get("event"), str),
            "sender output is not an event object",
        )
        require(
            event["event"] in ("state", "credential", "stats"),
            "sender emitted an unsupported event type",
        )
        require(
            not (
                set(event)
                - {
                    "event",
                    "state",
                    "message",
                    "kind",
                    "device_id",
                    "device_name",
                    "width",
                    "height",
                    "codec",
                    "frames",
                    "bytes",
                }
            ),
            "sender event contains unsupported fields",
        )
        for key in ("message", "kind", "codec"):
            value = event.get(key, "")
            require(isinstance(value, str), f"event {key} is not a string")
            require(
                PIN not in value and WRONG_PIN not in value,
                "dummy credential leaked into a public event",
            )
        counters = event["event"] == "stats" or "frames" in event or "bytes" in event
        if counters:
            frames, payload = event.get("frames", 0), event.get("bytes", 0)
            require(
                type(frames) is int
                and type(payload) is int
                and frames >= 0
                and payload >= 0,
                "sender counters must be nonnegative integers",
            )
        if self.awaiting_start:
            require(len(self.events) < 5000, "sender emitted too many events")
            if event["event"] != "state" or event.get("state") != "connecting":
                self.events.append(event)
                return
            self.awaiting_start = False
        if event.get("state"):
            self.state = event["state"]
            if self.state != "pairing":
                self.pending = None
        if event["event"] == "credential":
            require(
                self.state == "pairing"
                and event.get("kind") in ("pin", "password", "pin_or_password"),
                "invalid credential prompt",
            )
            self.pending = event["kind"]
            self.prompts += 1
            require(self.prompts <= 12, "sender kept requesting credentials")
        if counters:
            require(
                frames >= self.frames and payload >= self.payload_bytes,
                "transmitted counters moved backwards",
            )
            self.frames, self.payload_bytes = frames, payload
        if event.get("width") and event.get("height") and event.get("codec"):
            self.metadata = {key: event[key] for key in ("width", "height", "codec")}
        self.events.append(event)
        require(len(self.events) <= 5000, "sender emitted too many events")

    def pump(self, deadline, allow_eof=False):
        _, line = self.child.next_line(deadline, allow_eof=allow_eof)
        if line is None:
            return None
        try:
            value = json.loads(line)
        except (ValueError, UnicodeError):
            raise Failure("sender stdout is not newline-delimited JSON") from None
        require(isinstance(value, dict), "sender stdout contains a non-object")
        require(
            self.controller or "probe" not in value,
            "raw engine emitted a controller probe record",
        )
        if "probe" not in value:
            self.accept(value)
        elif "snapshot" in value:
            self.accept(value["snapshot"])
        return value

    def exited(self, expected_exit=0):
        deadline = time.monotonic() + 8
        while self.pump(deadline, allow_eof=True) is not None:
            pass
        self.child.wait(
            timeout=max(0.001, deadline - time.monotonic()), expected_exit=expected_exit
        )
        require(
            self.state == ("idle" if expected_exit == 0 else "failed"),
            "sender exited without the expected terminal state",
        )

    def request(self, command, ok=True, **fields):
        self.child.send(dict(command=command, **fields))
        if not self.controller:
            return {}
        deadline = time.monotonic() + 8
        while True:
            value = self.pump(deadline)
            if value.get("probe") == "reply":
                require(
                    value.get("command") == command, "controller reply order changed"
                )
                require(
                    value.get("ok") is ok,
                    f"controller {command} returned an unexpected result: {value.get('error', '')}",
                )
                return value

    def start(self, port, recover=False):
        self.reset()
        self.awaiting_start = self.controller
        fields = dict(
            host="127.0.0.1",
            port=port,
            quality=self.suite.args.quality,
            fps=self.suite.args.fps,
            audio=False,
            cursor=True,
        )
        if not recover:
            self.request("start", **fields)
            return
        deadline = time.monotonic() + 8
        while True:
            self.child.send(dict(command="start", **fields))
            while True:
                reply = self.pump(deadline)
                if reply.get("probe") == "reply":
                    require(
                        reply.get("command") == "start", "unexpected recovery reply"
                    )
                    break
            if reply.get("ok"):
                return
            require(
                "active or stopping" in reply.get("error", ""),
                f"controller could not restart: {reply.get('error', '')}",
            )
            require(
                time.monotonic() < deadline,
                "controller did not release its previous engine",
            )
            time.sleep(0.025)

    def until(self, predicate, deadline, credential=PIN, allow_failed=False):
        while True:
            if self.state == "failed" and not allow_failed:
                raise Failure("sender reported failed; see redacted event log")
            if predicate():
                return
            if self.pending is not None and credential is not None:
                self.pending = None
                self.request("credential", value=credential)
            else:
                self.pump(deadline)

    def progress(self, deadline):
        self.until(
            lambda: (
                self.state == "streaming" and self.frames > 0 and self.payload_bytes > 0
            ),
            deadline,
        )
        first = self.frames
        self.until(lambda: self.frames > first, deadline)
        require(
            self.metadata.get("width", 0) > 0
            and self.metadata.get("height", 0) > 0
            and self.metadata.get("codec"),
            "streaming omitted negotiated video metadata",
        )

    def stop(self):
        reply = self.request("stop")
        if self.controller:
            require(
                reply["duration_ms"] < 250,
                "Controller.Stop blocked for 250ms or longer",
            )
        self.until(lambda: self.state == "idle", time.monotonic() + 8, credential=None)
        if not self.controller:
            self.exited()

    def finish_controller(self):
        self.request("close")
        self.request("available", ok=False)
        self.child.eof()
        self.exited()


class Suite:
    def __init__(self, args, output):
        self.args, self.output = args, output
        self.children = []
        self.case_children = None
        self.case_receivers = None
        self.case_record = None
        allowed_environment = (
            "PATH",
            "LANG",
            "LC_ALL",
            "LC_CTYPE",
            "TZ",
            "LD_LIBRARY_PATH",
            "GST_PLUGIN_PATH",
            "GST_PLUGIN_SYSTEM_PATH",
            "GST_PLUGIN_PATH_1_0",
            "GST_PLUGIN_SYSTEM_PATH_1_0",
        )
        self.env = {
            key: os.environ[key] for key in allowed_environment if key in os.environ
        }
        self.env["GOMAXPROCS"] = "2"
        self.report = {
            "status": "running",
            "boundary": "synthetic loopback receiver, real Linux X11 capture and sender process",
            "quality": args.quality,
            "fps": args.fps,
            "cases": [],
            "children": [],
            "gaps": [
                "Transport/decrypted-packet counters do not prove picture decode or TV output.",
                "No Windows runtime, physical receiver, native GUI, system audio or mDNS discovery acceptance claim.",
            ],
        }

    def spawn(self, name, args, env=None):
        prefix = self.case_record["name"] + "-" if self.case_record else ""
        child = Child(prefix + name, args, env or self.env, self.output)
        (
            self.case_children if self.case_children is not None else self.children
        ).append(child)
        self.report["children"].append(child.record)
        return child

    @contextlib.contextmanager
    def case(self, name):
        self.case_record = {
            "name": name,
            "status": "running",
            "sessions": [],
            "receivers": [],
        }
        self.case_children = []
        self.case_receivers = []
        self.report["cases"].append(self.case_record)
        started = time.monotonic()
        print(f"RUN {name}", flush=True)
        try:
            yield
            self.case_record["status"] = "passed"
        except Exception as error:
            self.case_record.update(status="failed", error=redact(str(error)))
            raise
        finally:
            cleanup_errors = close_children(self.case_children)
            for receiver in self.case_receivers:
                try:
                    receiver.shutdown()
                    receiver.verify_sessions(self.args.require_legacy_decryption)
                except Exception as error:
                    cleanup_errors.append(
                        f"{receiver.child.name} final validation failed: {error}"
                    )
            passed = self.case_record["status"] == "passed"
            self.case_record["seconds"] = round(time.monotonic() - started, 3)
            if cleanup_errors:
                self.case_record.update(status="failed", cleanup_errors=cleanup_errors)
            print(f"{self.case_record['status'].upper()} {name}", flush=True)
            self.case_children = None
            self.case_receivers = None
            self.case_record = None
            if passed and cleanup_errors:
                raise Failure("; ".join(cleanup_errors))

    def transport(self, peer, receiver, baseline, deadline):
        require(
            not receiver.sessions
            or receiver.sessions[-1]["result"]["sender_shutdown_checked"],
            "previous receiver session has no shutdown checkpoint before reconnect",
        )
        peer.progress(deadline)
        stats = receiver.received(baseline, deadline)
        if (
            receiver.profile in LEGACY_DECRYPTION_PROFILES
            and "video_decrypted" in stats
            and stats["video_decrypted"] <= baseline.get("video_decrypted", 0)
        ):
            stats = receiver.fresh_stats()
        result = validate_transport(
            peer.frames,
            peer.payload_bytes,
            stats,
            baseline,
            receiver.profile,
            self.args.require_legacy_decryption,
        )
        result["negotiated_video"] = peer.metadata
        result["receiver_progress"] = result.pop("receiver_after")
        result["sender_shutdown_checked"] = False
        result["receiver_shutdown_checked"] = False
        self.case_record["sessions"].append(result)
        receiver.sessions.append(
            {"peer": peer, "baseline": dict(baseline), "result": result}
        )

    def finish_transport(self, peer, receiver):
        receiver.finish_session(peer, self.args.require_legacy_decryption)

    def raw_stream(self, profile, auth, eof):
        with self.case(f"engine-{profile}-{auth}-{'eof' if eof else 'stop'}"):
            receiver = Receiver(self, "receiver", profile, auth)
            baseline = receiver.fresh_stats()
            peer = Peer(self, "engine")
            peer.start(receiver.port)
            self.transport(
                peer, receiver, baseline, time.monotonic() + self.args.timeout
            )
            if auth != "none":
                require(
                    peer.prompts > 0,
                    "authenticated fixture never requested a credential",
                )
            if eof:
                peer.child.eof()
                peer.until(
                    lambda: peer.state == "idle", time.monotonic() + 8, credential=None
                )
                peer.exited()
            else:
                peer.stop()
            self.finish_transport(peer, receiver)

    def pairing_abort(self, eof):
        with self.case("engine-pairing-" + ("eof" if eof else "stop")):
            receiver = Receiver(self, "receiver", "modern", "pin")
            peer = Peer(self, "engine")
            peer.start(receiver.port)
            peer.until(
                lambda: peer.pending is not None,
                time.monotonic() + self.args.timeout,
                credential=None,
            )
            if eof:
                peer.child.eof()
                peer.until(
                    lambda: peer.state == "idle", time.monotonic() + 8, credential=None
                )
                peer.exited()
            else:
                peer.stop()
            require(
                peer.frames == 0 and receiver.fresh_stats()["video_bytes"] == 0,
                "pairing cancellation transmitted video",
            )
            receiver.no_video = True

    def wrong_pin(self, controller):
        with self.case(
            ("controller" if controller else "engine")
            + "-wrong-pin"
            + ("-reconnect" if controller else "")
        ):
            receiver = Receiver(self, "receiver", "modern", "pin")
            peer = Peer(self, "sender", controller)
            peer.start(receiver.port)
            peer.until(
                lambda: peer.state == "failed",
                time.monotonic() + self.args.timeout,
                credential=WRONG_PIN,
                allow_failed=True,
            )
            require(
                peer.prompts > 0
                and peer.frames == 0
                and not any(event.get("state") == "streaming" for event in peer.events),
                "wrong PIN reached streaming or skipped credential validation",
            )
            baseline = receiver.fresh_stats()
            require(
                baseline["video_bytes"] == 0, "wrong PIN sent video to the receiver"
            )
            if controller:
                peer.start(receiver.port, recover=True)
                self.transport(
                    peer, receiver, baseline, time.monotonic() + self.args.timeout
                )
                peer.stop()
                self.finish_transport(peer, receiver)
                peer.finish_controller()
            else:
                peer.exited(expected_exit=1)
                receiver.no_video = True

    def controller_reconnect(self):
        with self.case("controller-pin-stop-reconnect"):
            receiver = Receiver(self, "receiver", "modern", "pin")
            peer = Peer(self, "controller", True)
            peer.request("credential", ok=False, value=PIN)
            for _ in range(2):
                baseline = receiver.fresh_stats()
                peer.start(receiver.port)
                peer.until(
                    lambda: peer.pending is not None,
                    time.monotonic() + self.args.timeout,
                    credential=None,
                )
                peer.request("credential", ok=False, value="invalid-dummy-pin")
                peer.request(
                    "start",
                    ok=False,
                    host="127.0.0.1",
                    port=receiver.port,
                    quality=self.args.quality,
                    fps=self.args.fps,
                    cursor=True,
                )
                self.transport(
                    peer, receiver, baseline, time.monotonic() + self.args.timeout
                )
                peer.stop()
                self.finish_transport(peer, receiver)
                snapshot = peer.request("snapshot")["snapshot"]
                require(
                    snapshot.get("state") == "idle" and not snapshot.get("kind"),
                    "stopped controller retained pairing state",
                )
                peer.request("credential", ok=False, value=PIN)
            peer.request("close")
            peer.request("available", ok=False)
            peer.request(
                "start",
                ok=False,
                host="127.0.0.1",
                port=receiver.port,
                quality=self.args.quality,
                fps=self.args.fps,
                cursor=True,
            )
            peer.child.eof()
            peer.exited()

    def controller_shutdown(self, action, pairing=False):
        with self.case(f"controller-{action}-{'pairing' if pairing else 'streaming'}"):
            receiver = Receiver(
                self, "receiver", "modern", "pin" if pairing else "none"
            )
            baseline = receiver.fresh_stats()
            peer = Peer(self, "controller", True)
            peer.start(receiver.port)
            if pairing:
                peer.until(
                    lambda: peer.pending is not None,
                    time.monotonic() + self.args.timeout,
                    credential=None,
                )
            else:
                self.transport(
                    peer, receiver, baseline, time.monotonic() + self.args.timeout
                )
            peer.request(action)
            if action == "close":
                require(
                    peer.request("snapshot")["snapshot"].get("state") == "idle",
                    "Close did not release the active session",
                )
            else:
                peer.until(
                    lambda: peer.state == "idle", time.monotonic() + 8, credential=None
                )
            peer.finish_controller()
            if pairing:
                receiver.no_video = True
            else:
                self.finish_transport(peer, receiver)

    def receiver_loss(self):
        with self.case("controller-receiver-loss-reconnect"):
            receiver = Receiver(self, "receiver-first", "modern", "none")
            baseline = receiver.fresh_stats()
            peer = Peer(self, "controller", True)
            peer.start(receiver.port)
            self.transport(
                peer, receiver, baseline, time.monotonic() + self.args.timeout
            )
            receiver.shutdown()
            peer.until(
                lambda: peer.state == "failed",
                time.monotonic() + self.args.timeout,
                allow_failed=True,
            )
            self.finish_transport(peer, receiver)
            receiver = Receiver(self, "receiver-second", "modern", "none")
            baseline = receiver.fresh_stats()
            peer.start(receiver.port, recover=True)
            self.transport(
                peer, receiver, baseline, time.monotonic() + self.args.timeout
            )
            peer.stop()
            self.finish_transport(peer, receiver)
            peer.finish_controller()

    def protocol_errors(self):
        for name, data in (
            ("malformed", b"{not-json}\n"),
            ("oversized", b"x" * (MAX_LINE + 1) + b"\n"),
        ):
            with self.case("engine-" + name + "-input"):
                peer = Peer(self, "engine")
                peer.child.write(data)
                peer.until(
                    lambda: peer.state == "failed",
                    time.monotonic() + 8,
                    allow_failed=True,
                )
                peer.exited(expected_exit=1)
                require(peer.frames == 0, "invalid input transmitted frames")
        with self.case("engine-refused-loopback-connection"):
            with socket.socket() as reservation:
                reservation.bind(("127.0.0.1", 0))
                peer = Peer(self, "engine")
                peer.start(reservation.getsockname()[1])
                peer.until(
                    lambda: peer.state == "failed",
                    time.monotonic() + self.args.timeout,
                    allow_failed=True,
                )
                peer.exited(expected_exit=1)
                require(
                    peer.frames == 0, "refused connection reported transmitted frames"
                )

    def run(self):
        for profile in self.args.profiles:
            self.raw_stream(profile, "none", False)
            self.raw_stream(profile, "pin", True)
            self.raw_stream(profile, "combined", False)
        self.wrong_pin(False)
        self.pairing_abort(False)
        self.pairing_abort(True)
        self.protocol_errors()
        self.controller_reconnect()
        self.wrong_pin(True)
        self.receiver_loss()
        self.controller_shutdown("cancel")
        self.controller_shutdown("close", pairing=True)
        self.controller_shutdown("close")


def command(suite, name, args, timeout=10):
    child = suite.spawn(name, args)
    child.eof()
    child.wait(timeout=timeout)
    return child


def private_desktop(suite, runtime):
    width, height = png_dimensions(suite.args.desktop_image)
    for key in (
        "DISPLAY",
        "WAYLAND_DISPLAY",
        "DBUS_SESSION_BUS_ADDRESS",
        "DBUS_SESSION_BUS_PID",
        "XAUTHORITY",
        "PULSE_SERVER",
        "PIPEWIRE_REMOTE",
    ):
        suite.env.pop(key, None)
    for key, suffix in (
        ("HOME", "home"),
        ("XDG_CONFIG_HOME", "config"),
        ("XDG_DATA_HOME", "data"),
        ("XDG_CACHE_HOME", "cache"),
        ("XDG_RUNTIME_DIR", "runtime"),
    ):
        path = runtime / suffix
        path.mkdir(mode=0o700)
        suite.env[key] = str(path)
    suite.env.update(
        XDG_SESSION_TYPE="x11",
        XDG_CURRENT_DESKTOP="AircastSynthetic",
        DBUS_SYSTEM_BUS_ADDRESS=f"unix:path={runtime}/no-system-bus",
        SDL_AUDIODRIVER="dummy",
        SDL_RENDER_DRIVER="software",
        LIBGL_ALWAYS_SOFTWARE="1",
    )
    suite.env["GST_REGISTRY"] = str(runtime / "gstreamer-registry.bin")
    bus = suite.spawn(
        "private-dbus",
        [
            "dbus-daemon",
            "--session",
            "--nofork",
            "--print-address=1",
            f"--address=unix:path={runtime}/session-bus",
        ],
    )
    _, address = bus.next_line(time.monotonic() + 8)
    require(
        address.startswith(f"unix:path={runtime}/session-bus"),
        "D-Bus did not use the private UNIX socket",
    )
    suite.env["DBUS_SESSION_BUS_ADDRESS"] = address
    authority = runtime / "Xauthority"
    authority.touch(mode=0o600)
    cookie = secrets.token_hex(16)
    # Xvfb reads protocol/cookie records before choosing its display number.
    auth = subprocess.run(
        ["xauth", "-f", str(authority), "add", ":0", "MIT-MAGIC-COOKIE-1", cookie],
        env=suite.env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        timeout=5,
    )
    require(auth.returncode == 0, "could not create private Xauthority")
    suite.env["XAUTHORITY"] = str(authority)
    server = suite.spawn(
        "private-xvfb",
        [
            "Xvfb",
            "-displayfd",
            "1",
            "-screen",
            "0",
            f"{width}x{height}x24",
            "-nolisten",
            "tcp",
            "-noreset",
            "-auth",
            str(authority),
        ],
    )
    _, display = server.next_line(time.monotonic() + 8)
    require(display.isdigit(), "Xvfb did not return a display number")
    suite.env["DISPLAY"] = ":" + display
    auth = subprocess.run(
        [
            "xauth",
            "-f",
            str(authority),
            "add",
            ":" + display,
            "MIT-MAGIC-COOKIE-1",
            cookie,
        ],
        env=suite.env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        timeout=5,
    )
    require(auth.returncode == 0, "could not authorize the private display")
    command(suite, "private-display-info", ["xdpyinfo"])
    title = "Aircast SYNTHETIC integration chart"
    chart = suite.spawn(
        "desktop-chart",
        [
            "ffplay",
            "-loglevel",
            "warning",
            "-nostats",
            "-an",
            "-sn",
            "-loop",
            "0",
            "-x",
            str(width),
            "-y",
            str(height),
            "-left",
            "0",
            "-top",
            "0",
            "-fs",
            "-noborder",
            "-window_title",
            title,
            "-i",
            str(suite.args.desktop_image),
        ],
    )
    deadline = time.monotonic() + 10
    while True:
        require(
            chart.process.poll() is None, "chart viewer exited before screen capture"
        )
        result = subprocess.run(
            ["xwininfo", "-root", "-tree"],
            env=suite.env,
            capture_output=True,
            timeout=2,
        )
        if result.returncode == 0 and any(
            title.encode() in line and f"{width}x{height}+0+0".encode() in line
            for line in result.stdout.splitlines()
        ):
            break
        require(time.monotonic() < deadline, "synthetic chart window did not appear")
        time.sleep(0.1)
    screenshot = suite.output / "source-desktop.png"
    for attempt in range(10):
        candidate = suite.output / f"source-candidate-{attempt}.png"
        command(
            suite,
            f"capture-source-desktop-{attempt}",
            [
                "ffmpeg",
                "-hide_banner",
                "-loglevel",
                "error",
                "-nostdin",
                "-threads",
                "2",
                "-f",
                "x11grab",
                "-video_size",
                f"{width}x{height}",
                "-i",
                suite.env["DISPLAY"],
                "-frames:v",
                "1",
                "-threads",
                "2",
                "-update",
                "1",
                str(candidate),
            ],
        )
        comparison = command(
            suite,
            f"compare-source-desktop-{attempt}",
            [
                "ffmpeg",
                "-hide_banner",
                "-nostdin",
                "-threads",
                "2",
                "-i",
                str(suite.args.desktop_image),
                "-i",
                str(candidate),
                "-filter_complex_threads",
                "1",
                "-filter_complex",
                "[0:v]format=gbrp[a];[1:v]format=gbrp[b];[a][b]psnr=stats_file=-",
                "-frames:v",
                "1",
                "-f",
                "null",
                "-",
            ],
        )
        _, statistics = comparison.next_line(time.monotonic() + 2)
        psnr = parse_desktop_psnr(statistics)
        comparison.close()
        if float(psnr) >= 30:
            candidate.rename(screenshot)
            break
        time.sleep(0.1)
    require(
        screenshot.exists(),
        "private chart did not render at the expected geometry and pixels",
    )
    suite.report["desktop"] = {
        "width": width,
        "height": height,
        "fixture": str(suite.args.desktop_image),
        "fixture_sha256": hashlib.sha256(
            suite.args.desktop_image.read_bytes()
        ).hexdigest(),
        "source_screenshot": "source-desktop.png",
        "source_rgb_psnr_db": psnr,
        "capture": "real private Xvfb X11 desktop; no videotestsrc substitution",
    }


def decode_clip(suite):
    if suite.args.outgoing_clip is None:
        suite.report["outgoing_clip_decode"] = {
            "status": "not run",
            "reason": "no outgoing encoded clip fixture supplied; receiver only counts packets",
        }
        return
    clip = suite.args.outgoing_clip
    output = suite.output / "outgoing-clip.framemd5"
    command(
        suite,
        "decode-outgoing-clip",
        [
            "ffmpeg",
            "-hide_banner",
            "-loglevel",
            "error",
            "-nostdin",
            "-xerror",
            "-err_detect",
            "explode",
            "-threads",
            "2",
            "-i",
            str(clip),
            "-map",
            "0:v:0",
            "-an",
            "-frames:v",
            "300",
            "-threads",
            "2",
            "-f",
            "framemd5",
            str(output),
        ],
        timeout=60,
    )
    frames = sum(
        bool(line.strip()) and not line.startswith("#")
        for line in output.read_text().splitlines()
    )
    require(frames > 0, "outgoing clip decoded no pictures")
    suite.report["outgoing_clip_decode"] = {
        "status": "passed",
        "fixture": str(clip),
        "fixture_sha256": hashlib.sha256(clip.read_bytes()).hexdigest(),
        "decoded_frames": frames,
        "frame_limit": 300,
        "scope": "caller-supplied clip only; no automatic proof it is this session's outgoing stream or a TV's output",
    }


def arguments(argv=None):
    parser = argparse.ArgumentParser(
        description="Real Aircast sender + root controller acceptance against synthetic loopback receivers. Run via run.sh in the Linux VM."
    )
    parser.add_argument("--engine", type=Path, required=True)
    parser.add_argument("--receiver", type=Path, required=True)
    parser.add_argument("--controller-probe", type=Path, required=True)
    parser.add_argument(
        "--desktop-image",
        type=Path,
        required=True,
        help="caller-owned quality chart PNG; displayed on a new private Xvfb desktop",
    )
    parser.add_argument(
        "--output",
        type=Path,
        required=True,
        help="new artifact directory; existing paths are rejected",
    )
    parser.add_argument(
        "--profiles",
        nargs="+",
        choices=PROFILES,
        default=["modern", "roku", "appletv3"],
    )
    parser.add_argument("--quality", choices=("quality", "balanced"), default="quality")
    parser.add_argument(
        "--fps", type=int, choices=range(1, 31), default=30, metavar="1..30"
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=45,
        help="seconds per negotiation/transmission operation, 5..120",
    )
    parser.add_argument(
        "--overall-timeout",
        type=int,
        default=900,
        help="whole suite deadline in seconds, 30..3600",
    )
    parser.add_argument(
        "--require-legacy-decryption",
        action="store_true",
        help="fail if appletv3/uxplay receiver CLI omits decrypted counters",
    )
    parser.add_argument(
        "--outgoing-clip",
        type=Path,
        help="optional caller-supplied outgoing Annex B/container video with decoder configuration",
    )
    args = parser.parse_args(argv)
    require(
        5 <= args.timeout <= 120 and 30 <= args.overall_timeout <= 3600,
        "timeouts are outside the supported bounds",
    )
    require(
        len(args.profiles) == len(set(args.profiles)), "duplicate receiver profiles"
    )
    for key in (
        "engine",
        "receiver",
        "controller_probe",
        "desktop_image",
        "outgoing_clip",
    ):
        value = getattr(args, key)
        if value is not None:
            value = value.resolve(strict=True)
            require(value.is_file(), f"{key} is not a regular file")
            if key in ("engine", "receiver", "controller_probe"):
                require(os.access(value, os.X_OK), f"{key} is not executable")
            setattr(args, key, value)
    require(
        args.controller_probe.parent / "aircast-engine" == args.engine,
        "copy the real engine beside controller-probe as aircast-engine; do not use a symlink",
    )
    if args.require_legacy_decryption:
        require(
            bool(set(args.profiles) & LEGACY_DECRYPTION_PROFILES),
            "strict legacy decryption needs an appletv3 or uxplay profile",
        )
    args.output = args.output.absolute()
    png_dimensions(args.desktop_image)
    return args


def main(argv=None):
    args = arguments(argv)
    require(
        sys.platform == "linux",
        "run acceptance tests in the disposable Linux VM, never on the personal Mac desktop",
    )
    for tool in (
        "ip",
        "dbus-daemon",
        "Xvfb",
        "xauth",
        "xdpyinfo",
        "xwininfo",
        "ffplay",
        "ffmpeg",
    ):
        require(shutil.which(tool), f"missing Linux test runtime: {tool}")
    interfaces = json.loads(
        subprocess.check_output(
            ["ip", "-details", "-json", "address", "show"], timeout=5
        )
    )
    require(
        isolated_network(interfaces),
        "refusing a networked namespace; run through scripts/integration/run.sh",
    )
    subprocess.run(["ip", "link", "set", "lo", "up"], check=True, timeout=5)
    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    suite = Suite(args, args.output)
    previous_handlers = {}

    def interrupted(number, frame):
        raise Failure(f"acceptance suite interrupted by signal {number}")

    for number in (signal.SIGINT, signal.SIGTERM, signal.SIGALRM):
        previous_handlers[number] = signal.signal(number, interrupted)
    signal.alarm(args.overall_timeout)
    try:
        with tempfile.TemporaryDirectory(
            prefix="private-desktop-", dir=args.output
        ) as directory:
            try:
                private_desktop(suite, Path(directory))
                suite.run()
                decode_clip(suite)
                require(
                    all(case["status"] == "passed" for case in suite.report["cases"]),
                    "a child required forced termination",
                )
                suite.report["status"] = "passed"
            finally:
                cleanup_errors = close_children(suite.children)
                require(not cleanup_errors, "; ".join(cleanup_errors))
        require(
            not any(child["forced_kill"] for child in suite.report["children"]),
            "a test child required SIGKILL during cleanup",
        )
    except Exception as error:
        suite.report.update(status="failed", error=redact(str(error)))
    finally:
        signal.alarm(0)
        for number, handler in previous_handlers.items():
            signal.signal(number, handler)
        (args.output / "report.json").write_text(
            json.dumps(suite.report, indent=2) + "\n"
        )
    print(
        f"{suite.report['status'].upper()}: {args.output / 'report.json'}", flush=True
    )
    if suite.report["status"] != "passed":
        print(suite.report.get("error", "acceptance failed"), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (Failure, OSError, subprocess.SubprocessError) as error:
        print(redact(str(error)), file=sys.stderr)
        raise SystemExit(2)
