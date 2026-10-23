# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///

import argparse
import contextlib
import copy
import io
import json
from pathlib import Path
import signal
import struct
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest

import run


class IsolationTests(unittest.TestCase):
    def interfaces(self):
        return [{"ifname": "lo", "link_type": "loopback"}] + [
            {
                "ifname": kind + "0",
                "operstate": "DOWN",
                "flags": ["NOARP"],
                "addr_info": [],
                "linkinfo": {
                    "info_kind": kind,
                    "info_data": {"local": "any", "remote": "any"},
                },
            }
            for kind in ("gre", "gretap", "erspan")
        ]

    def test_loopback_only(self):
        self.assertTrue(run.isolated_network(self.interfaces()[:1]))

    def test_inert_kernel_fallback_tunnels(self):
        self.assertTrue(run.isolated_network(self.interfaces()))

    def test_active_or_addressed_tunnel_is_rejected(self):
        for change in (
            {"operstate": "UP"},
            {"flags": ["UP"]},
            {"addr_info": [{"local": "192.0.2.1"}]},
            {"link": "eth0"},
            {"master": "br0"},
        ):
            with self.subTest(change=change):
                interfaces = self.interfaces()
                interfaces[1].update(change)
                self.assertFalse(run.isolated_network(interfaces))

    def test_configured_tunnel_is_rejected(self):
        for key in ("local", "remote"):
            interfaces = self.interfaces()
            interfaces[1]["linkinfo"]["info_data"][key] = "192.0.2.1"
            self.assertFalse(run.isolated_network(interfaces))

    def test_other_interfaces_are_rejected_even_when_down(self):
        for name in ("eth0", "gre1", "wlan0"):
            interfaces = self.interfaces()
            other = copy.deepcopy(interfaces[1])
            other["ifname"] = name
            interfaces.append(other)
            self.assertFalse(run.isolated_network(interfaces))

    def test_missing_or_invalid_loopback_is_rejected(self):
        self.assertFalse(run.isolated_network(self.interfaces()[1:]))
        self.assertFalse(run.isolated_network([{"ifname": "lo", "link_type": "ether"}]))


class StatsTests(unittest.TestCase):
    def test_upstream_stats(self):
        line = "stats: connections=1 info=1 pairing=3/2 fairplay=0 setup=4 feedback=1 teardown=0 events=1 video=67/54321B audio=0/0B timing=3/3 digest_challenges=0"
        self.assertEqual(
            run.parse_receiver_stats(line), {"video_packets": 67, "video_bytes": 54321}
        )

    def test_optional_decryption_fields(self):
        line = "final stats: video=99/54321B video_decrypted=90 video_crypto_errors=0 video_malformed=0 video_frames=90"
        stats = run.parse_receiver_stats(line)
        self.assertEqual(stats["video_decrypted"], 90)
        self.assertEqual(stats["video_crypto_errors"], 0)
        self.assertEqual(stats["video_frames"], 90)

    def test_short_decryption_aliases(self):
        self.assertEqual(
            run.parse_receiver_stats(
                "stats: video=4/800B decrypted=3 crypto_errors=1 malformed=2"
            )["video_crypto_errors"],
            1,
        )

    def test_listen_line_is_not_stats(self):
        self.assertIsNone(
            run.parse_receiver_stats(
                "doubletake test receiver listening on 127.0.0.1:34567 (profile=modern auth=pin)"
            )
        )

    def test_positive_transmission_and_receiver_deltas(self):
        result = run.validate_transport(
            10, 5000, {"video_packets": 12, "video_bytes": 6000}, {}, "modern", False
        )
        self.assertEqual(result["transmitted_frames"], 10)
        self.assertIn("not implemented", result["decryption_check"])

    def test_previous_session_stats_do_not_pass(self):
        stats = {"video_packets": 10, "video_bytes": 5000}
        with self.assertRaisesRegex(run.Failure, "did not increase"):
            run.validate_transport(10, 5000, stats, stats, "modern", False)

    def test_sender_only_stats_do_not_pass(self):
        with self.assertRaises(run.Failure):
            run.validate_transport(
                10, 5000, {"video_packets": 0, "video_bytes": 0}, {}, "modern", False
            )

    def test_receiver_only_stats_do_not_pass(self):
        with self.assertRaises(run.Failure):
            run.validate_transport(
                0, 0, {"video_packets": 10, "video_bytes": 5000}, {}, "modern", False
            )

    def test_legacy_decryption(self):
        stats = {
            "video_packets": 10,
            "video_bytes": 5000,
            "video_decrypted": 8,
            "video_crypto_errors": 0,
        }
        self.assertIn(
            "AVCC",
            run.validate_transport(8, 4000, stats, {}, "appletv3", True)[
                "decryption_check"
            ],
        )
        with self.assertRaisesRegex(run.Failure, "decrypted no new"):
            run.validate_transport(
                8, 4000, stats, {"video_decrypted": 8}, "uxplay", False
            )

    def test_missing_legacy_decryption_is_a_gap_or_strict_failure(self):
        stats = {"video_packets": 10, "video_bytes": 5000}
        self.assertIn(
            "not exposed",
            run.validate_transport(8, 4000, stats, {}, "appletv3", False)[
                "decryption_check"
            ],
        )
        with self.assertRaisesRegex(run.Failure, "absent"):
            run.validate_transport(8, 4000, stats, {}, "appletv3", True)

    def test_crypto_errors_fail(self):
        with self.assertRaisesRegex(run.Failure, "crypto_errors"):
            run.validate_transport(
                8,
                4000,
                {"video_packets": 10, "video_bytes": 5000, "video_crypto_errors": 1},
                {},
                "modern",
                False,
            )

    def test_receiver_retains_errors_missing_from_later_stats(self):
        receiver = object.__new__(run.Receiver)
        receiver.stats = {"video_packets": 0, "video_bytes": 0}
        receiver.observe("stats: video=3/300B video_crypto_errors=1 video_malformed=1")
        receiver.observe("final stats: video=4/400B video_crypto_errors=0")
        self.assertEqual(receiver.stats["video_crypto_errors"], 1)
        self.assertEqual(receiver.stats["video_malformed"], 1)
        with self.assertRaisesRegex(run.Failure, "video_crypto_errors"):
            run.validate_receiver_errors(receiver.stats)


class EventTests(unittest.TestCase):
    def peer(self):
        peer = object.__new__(run.Peer)
        peer.events = []
        peer.reset()
        return peer

    def test_monotonic_transmitted_stats(self):
        peer = self.peer()
        peer.accept({"event": "stats", "frames": 10, "bytes": 5000})
        with self.assertRaisesRegex(run.Failure, "backwards"):
            peer.accept({"event": "stats", "frames": 9, "bytes": 5000})

    def test_recovery_ignores_queued_terminal_snapshot_until_connecting(self):
        peer = self.peer()
        peer.awaiting_start = True
        peer.accept({"event": "state", "state": "failed", "frames": 30, "bytes": 5000})
        self.assertEqual(peer.frames, 0)
        peer.accept({"event": "state", "state": "connecting"})
        peer.accept({"event": "stats", "state": "streaming", "frames": 1, "bytes": 200})
        self.assertEqual(peer.frames, 1)
        with self.assertRaisesRegex(run.Failure, "backwards"):
            peer.accept({"event": "stats", "frames": 0, "bytes": 0})

    def test_queued_old_snapshot_still_checks_secret_and_counter_types(self):
        for invalid in (
            {"event": "state", "state": "failed", "message": "PIN 1234"},
            {"event": "stats", "frames": True, "bytes": 100},
        ):
            peer = self.peer()
            peer.awaiting_start = True
            with self.assertRaises(run.Failure):
                peer.accept(invalid)

    def test_reset_allows_a_new_session(self):
        peer = self.peer()
        peer.accept({"event": "stats", "frames": 10, "bytes": 5000})
        peer.reset()
        peer.accept({"event": "stats", "frames": 1, "bytes": 100})
        self.assertEqual(peer.frames, 1)

    def test_reject_bool_as_counter(self):
        with self.assertRaisesRegex(run.Failure, "integers"):
            self.peer().accept({"event": "stats", "frames": True, "bytes": 1})

    def test_leaked_dummy_pin_fails_but_numeric_stats_do_not(self):
        peer = self.peer()
        peer.accept({"event": "stats", "frames": 1234, "bytes": 9999})
        with self.assertRaisesRegex(run.Failure, "leaked"):
            peer.accept(
                {"event": "state", "state": "pairing", "message": "PIN 1234 accepted"}
            )

    def test_credential_prompt_needs_pairing(self):
        with self.assertRaisesRegex(run.Failure, "invalid credential"):
            self.peer().accept(
                {"event": "credential", "state": "streaming", "kind": "pin"}
            )

    def test_streaming_state_alone_is_not_transmission(self):
        peer = self.peer()
        peer.accept(
            {
                "event": "state",
                "state": "streaming",
                "width": 1920,
                "height": 1080,
                "codec": "h264",
            }
        )
        self.assertEqual(peer.frames, 0)
        self.assertEqual(peer.metadata["width"], 1920)

    def test_unknown_event_fields_cannot_hide_a_secret(self):
        with self.assertRaisesRegex(run.Failure, "unsupported fields"):
            self.peer().accept({"event": "state", "state": "pairing", "value": "1234"})

    def test_terminal_controller_snapshot_keeps_final_sender_counters(self):
        peer = self.peer()
        peer.accept({"event": "stats", "frames": 2, "bytes": 200})
        peer.accept({"event": "state", "state": "idle", "frames": 3, "bytes": 300})
        self.assertEqual((peer.frames, peer.payload_bytes), (3, 300))


class FixtureTests(unittest.TestCase):
    def test_desktop_psnr_requires_a_real_metric(self):
        self.assertEqual(
            run.parse_desktop_psnr("n:1 mse_avg:5 psnr_avg:40.9 psnr_r:40"), "40.9"
        )
        self.assertEqual(
            run.parse_desktop_psnr("n:1 mse_avg:0 psnr_avg:inf psnr_r:inf"), "inf"
        )
        for text in (
            "",
            "n:1 psnr_avg:nan",
            "ffmpeg failed",
            "Input #0: average:inf.png",
        ):
            with self.assertRaises(run.Failure):
                run.parse_desktop_psnr(text)

    def test_metric_like_filenames_cannot_override_desktop_statistics(self):
        text = "Input #0, image2, from '/fixture/average:inf.png':\nn:1 mse_avg:1000 psnr_avg:18.13 psnr_r:18.13\n"
        self.assertEqual(run.parse_desktop_psnr(text), "18.13")
        self.assertLess(float(run.parse_desktop_psnr(text)), 30)

    def test_png_header_dimensions(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "chart.png"
            path.write_bytes(
                b"\x89PNG\r\n\x1a\n"
                + struct.pack(">I", 13)
                + b"IHDR"
                + struct.pack(">II", 1920, 1080)
            )
            self.assertEqual(run.png_dimensions(path), (1920, 1080))
            path.write_bytes(b"not a PNG")
            with self.assertRaises(run.Failure):
                run.png_dimensions(path)

    def test_log_redaction(self):
        self.assertEqual(
            run.redact("PIN=1234 wrong=9999 bytes=123456"),
            "PIN=[dummy credential redacted] wrong=[dummy credential redacted] bytes=123456",
        )


class ChildTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.children = []

    def tearDown(self):
        for child in reversed(self.children):
            child.close()
        self.directory.cleanup()

    def spawn(self, code):
        child = run.Child(
            "fixture-" + str(len(self.children)),
            [sys.executable, "-u", "-c", code],
            None,
            Path(self.directory.name),
        )
        self.children.append(child)
        return child

    def test_child_json_stdin_and_eof(self):
        child = self.spawn(
            "import json,sys\nfor line in sys.stdin:\n print(json.dumps({'event':'stats','frames':1,'bytes':len(line)}),flush=True)"
        )
        child.send({"command": "start"})
        _, line = child.next_line(time.monotonic() + 3)
        self.assertEqual(json.loads(line)["frames"], 1)
        child.eof()
        child.wait(timeout=3)

    def test_output_timeout_is_bounded(self):
        child = self.spawn("import time; time.sleep(30)")
        started = time.monotonic()
        with self.assertRaisesRegex(run.Failure, "timed out"):
            child.next_line(started + 0.05)
        self.assertLess(time.monotonic() - started, 1)

    def test_stdin_write_timeout_is_bounded(self):
        child = self.spawn("import time; time.sleep(30)")
        started = time.monotonic()
        with self.assertRaisesRegex(run.Failure, "write timed out"):
            child.write(b"x" * (1 << 20), timeout=0.05)
        self.assertLess(time.monotonic() - started, 1)

    def test_terminate_reaps_own_child(self):
        child = self.spawn("import time; time.sleep(30)")
        child.terminate()
        self.assertIsNotNone(child.process.poll())
        self.assertFalse(child.record["forced_kill"])

    def test_output_protocol_line_limit(self):
        child = self.spawn("print('x' * 70000, flush=True)")
        with self.assertRaisesRegex(run.Failure, "64 KiB"):
            child.next_line(time.monotonic() + 3)

    def test_stderr_log_redacts_dummy_credentials(self):
        child = self.spawn(
            "import sys; print('PIN=1234 password=9999',file=sys.stderr)"
        )
        child.wait(timeout=3)
        child.close()
        log = (Path(self.directory.name) / (child.name + ".stderr.log")).read_text()
        self.assertNotIn("1234", log)
        self.assertNotIn("9999", log)

    def test_nonzero_exit_is_not_success(self):
        child = self.spawn("raise SystemExit(7)")
        with self.assertRaisesRegex(run.Failure, "status 7"):
            child.wait(timeout=3)

    def failed_peer(self, ending):
        peer = object.__new__(run.Peer)
        peer.controller = False
        peer.events = []
        peer.reset()
        peer.child = self.spawn(
            "import json,os,signal,resource\nresource.setrlimit(resource.RLIMIT_CORE,(0,0))\nprint(json.dumps({'event':'state','state':'failed'}),flush=True)\n"
            + ending
        )
        peer.until(
            lambda: peer.state == "failed", time.monotonic() + 3, allow_failed=True
        )
        return peer

    def test_expected_engine_error_requires_normal_exit_one(self):
        peer = self.failed_peer("raise SystemExit(1)")
        peer.exited(expected_exit=1)
        self.assertEqual(peer.child.record["exit_status"], 1)

    def test_panic_style_exit_after_failed_is_not_an_expected_error(self):
        peer = self.failed_peer("raise SystemExit(2)")
        with self.assertRaisesRegex(run.Failure, "status 2, expected 1"):
            peer.exited(expected_exit=1)

    @unittest.skipUnless(
        sys.platform == "linux", "crash fixtures run only in the Linux VM"
    )
    def test_crash_after_failed_is_not_an_expected_error(self):
        for number in (signal.SIGSEGV, signal.SIGABRT):
            with self.subTest(signal=number):
                peer = self.failed_peer(f"os.kill(os.getpid(),{int(number)})")
                with self.assertRaisesRegex(run.Failure, "terminated by signal"):
                    peer.exited(expected_exit=1)

    @unittest.skipUnless(
        sys.platform == "linux", "crash fixtures run only in the Linux VM"
    )
    def test_crash_after_failed_marks_case_failed_and_cleans_children(self):
        suite = run.Suite(
            argparse.Namespace(quality="quality", fps=30), Path(self.directory.name)
        )
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(run.Failure, "terminated by signal"):
                with suite.case("crash-cleanup"):
                    sibling = suite.spawn(
                        "owned-sibling",
                        [sys.executable, "-u", "-c", "import time;time.sleep(30)"],
                    )
                    peer = object.__new__(run.Peer)
                    peer.controller = False
                    peer.events = []
                    peer.reset()
                    peer.child = suite.spawn(
                        "crashing-sender",
                        [
                            sys.executable,
                            "-u",
                            "-c",
                            "import json,os,signal,resource;resource.setrlimit(resource.RLIMIT_CORE,(0,0));print(json.dumps({'event':'state','state':'failed'}),flush=True);os.kill(os.getpid(),signal.SIGABRT)",
                        ],
                    )
                    peer.until(
                        lambda: peer.state == "failed",
                        time.monotonic() + 3,
                        allow_failed=True,
                    )
                    peer.exited(expected_exit=1)
        self.assertEqual(peer.state, "failed")
        self.assertEqual(suite.report["cases"][0]["status"], "failed")
        self.assertEqual(peer.child.record["exit_status"], -signal.SIGABRT)
        self.assertIsNotNone(sibling.process.poll())

    def test_transmission_wait_needs_stats_progress(self):
        peer = object.__new__(run.Peer)
        peer.controller = False
        peer.events = []
        peer.reset()
        peer.child = self.spawn(
            "import json,time\nfor event in [{'event':'state','state':'streaming','width':640,'height':360,'codec':'h264'},{'event':'stats','frames':1,'bytes':100},{'event':'stats','frames':2,'bytes':200}]:\n print(json.dumps(event),flush=True)\ntime.sleep(30)"
        )
        peer.progress(time.monotonic() + 3)
        self.assertEqual(peer.frames, 2)

    def test_trailing_events_after_idle_are_still_validated(self):
        peer = object.__new__(run.Peer)
        peer.controller = False
        peer.events = []
        peer.reset()
        peer.child = self.spawn(
            "import json\nprint(json.dumps({'event':'state','state':'idle'}),flush=True)\nprint(json.dumps({'event':'state','state':'idle','message':'PIN 1234'}),flush=True)"
        )
        peer.until(lambda: peer.state == "idle", time.monotonic() + 3)
        with self.assertRaisesRegex(run.Failure, "leaked"):
            peer.exited()

    def test_cleanup_continues_after_one_child_close_fails(self):
        first = self.spawn("import time; time.sleep(30)")

        class BrokenCleanup:
            name = "broken-fixture"
            failure = None
            record = {"forced_kill": False}

            def close(self):
                raise run.Failure("intentional cleanup error")

        errors = run.close_children([first, BrokenCleanup()])
        self.assertEqual(len(errors), 1)
        self.assertIsNotNone(first.process.poll())

    def test_case_failure_cleans_child(self):
        args = argparse.Namespace(quality="quality", fps=30)
        suite = run.Suite(args, Path(self.directory.name))
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(run.Failure, "intentional"):
                with suite.case("self-test"):
                    child = suite.spawn(
                        "own-child",
                        [sys.executable, "-u", "-c", "import time;time.sleep(30)"],
                    )
                    raise run.Failure("intentional test failure")
        self.assertIsNotNone(child.process.poll())
        self.assertEqual(suite.report["cases"][0]["status"], "failed")


class ReceiverFinalizationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.path = Path(self.directory.name)
        self.receiver_path = self.path / "synthetic-stats-receiver"
        self.receiver_path.write_text(
            "#!"
            + sys.executable
            + "\n"
            + """
import json
import os
import select
import signal
import sys

done = False
packets = 0
payload = 0
errors = {'video_crypto_errors': 0, 'video_malformed': 0}
final_errors = dict(errors)
omit_final = False

if sys.argv[sys.argv.index('-auth') + 1] == 'none' and os.environ.get('DOUBLETAKE_RECEIVER_CODE'):
    sys.exit('receiver code requires an auth mode')

def stop(number, frame):
    global done
    done = True

def summary(final=False):
    counters = dict(errors)
    if final:
        counters.update({key: max(counters[key], value) for key, value in final_errors.items()})
    return f"video={packets}/{payload}B " + ' '.join(f'{key}={value}' for key, value in counters.items())

signal.signal(signal.SIGTERM, stop)
print('doubletake test receiver listening on 127.0.0.1:45555 (synthetic stats fixture)', flush=True)
while not done:
    readable, _, _ = select.select([sys.stdin], [], [], 0.02)
    if readable:
        line = sys.stdin.readline()
        if line:
            command = json.loads(line)
            if command['fixture'] == 'video':
                packets += 3
                payload += 300
            elif command['fixture'] == 'error':
                target = final_errors if command.get('final_only') else errors
                target[command['counter']] = 1
            elif command['fixture'] == 'omit-final':
                omit_final = True
            print('fixture ack', flush=True)
    print('stats: ' + summary(), flush=True)
if not omit_final:
    print('final stats: ' + summary(True), flush=True)
"""
        )
        self.receiver_path.chmod(0o700)
        args = argparse.Namespace(
            quality="quality",
            fps=30,
            receiver=self.receiver_path,
            require_legacy_decryption=False,
        )
        self.suite = run.Suite(args, self.path)

    def tearDown(self):
        self.directory.cleanup()

    def control(self, receiver, action, **fields):
        receiver.child.send(dict(fixture=action, **fields))
        deadline = time.monotonic() + 3
        while True:
            _, line = receiver.child.next_line(deadline)
            if line == "fixture ack":
                return
            receiver.observe(line)

    def session(self, receiver, peer=None):
        baseline = receiver.fresh_stats()
        self.control(receiver, "video")
        if peer is None:
            peer = SimpleNamespace(
                controller=True,
                frames=2,
                payload_bytes=200,
                metadata={"width": 640, "height": 360, "codec": "h264"},
                progress=lambda deadline: None,
            )
        peer.state = "streaming"
        self.suite.transport(peer, receiver, baseline, time.monotonic() + 3)
        return peer

    def complete(self, peer, receiver):
        peer.state = "idle"
        self.suite.finish_transport(peer, receiver)

    def test_clean_final_stats_complete_the_case(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.suite.case("clean-final"):
                receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                peer = self.session(receiver)
                self.complete(peer, receiver)
        record = self.suite.report["cases"][0]
        self.assertEqual(record["status"], "passed")
        self.assertTrue(record["sessions"][0]["sender_shutdown_checked"])
        self.assertTrue(record["sessions"][0]["receiver_shutdown_checked"])
        self.assertEqual(record["receivers"][0]["final_stats"]["video_packets"], 3)
        self.assertIsNotNone(receiver.child.process.poll())

    def test_post_sender_checkpoint_uses_later_receiver_stats(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.suite.case("fresh-checkpoint"):
                receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                peer = self.session(receiver)
                self.control(receiver, "video")
                self.complete(peer, receiver)
        result = self.suite.report["cases"][0]["sessions"][0]
        self.assertEqual(result["receiver_progress"]["video_packets"], 3)
        self.assertEqual(result["receiver_post_sender"]["video_packets"], 6)
        self.assertEqual(result["receiver_after"]["video_packets"], 6)

    def late_error(self, counter, final_only):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(run.Failure, counter):
                with self.suite.case("late-error"):
                    receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                    peer = self.session(receiver)
                    if final_only:
                        self.complete(peer, receiver)
                    self.control(
                        receiver, "error", counter=counter, final_only=final_only
                    )
                    if not final_only:
                        self.complete(peer, receiver)
        record = self.suite.report["cases"][0]
        self.assertEqual(record["status"], "failed")
        self.assertEqual(record["sessions"][0]["receiver_progress"][counter], 0)
        self.assertEqual(record["receivers"][0]["final_stats"][counter], 1)
        self.assertIsNotNone(receiver.child.process.poll())

    def test_good_progress_then_final_crypto_error_fails(self):
        self.late_error("video_crypto_errors", True)

    def test_good_progress_then_final_malformed_error_fails(self):
        self.late_error("video_malformed", True)

    def test_good_progress_then_post_sender_crypto_error_fails(self):
        self.late_error("video_crypto_errors", False)

    def test_good_progress_then_post_sender_malformed_error_fails(self):
        self.late_error("video_malformed", False)

    def test_reconnect_series_keeps_each_shutdown_delta(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.suite.case("reconnect-checkpoints"):
                receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                peer = self.session(receiver)
                self.complete(peer, receiver)
                self.session(receiver, peer)
                self.complete(peer, receiver)
        results = self.suite.report["cases"][0]["sessions"]
        self.assertEqual(
            [result["receiver_before"]["video_packets"] for result in results], [0, 3]
        )
        self.assertEqual(
            [result["receiver_after"]["video_packets"] for result in results], [3, 6]
        )
        self.assertTrue(
            all(
                result["sender_shutdown_checked"]
                and result["receiver_shutdown_checked"]
                for result in results
            )
        )

    def test_reconnect_series_fails_on_receiver_final_error(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(run.Failure, "video_crypto_errors"):
                with self.suite.case("reconnect-final-error"):
                    receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                    peer = self.session(receiver)
                    self.complete(peer, receiver)
                    self.session(receiver, peer)
                    self.complete(peer, receiver)
                    self.control(
                        receiver,
                        "error",
                        counter="video_crypto_errors",
                        final_only=True,
                    )
        record = self.suite.report["cases"][0]
        self.assertEqual(record["status"], "failed")
        self.assertEqual(len(record["sessions"]), 2)
        self.assertTrue(
            all(result["sender_shutdown_checked"] for result in record["sessions"])
        )

    def test_missing_post_sender_checkpoint_fails(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(run.Failure, "post-sender-shutdown checkpoint"):
                with self.suite.case("missing-checkpoint"):
                    receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                    self.session(receiver)
        self.assertEqual(self.suite.report["cases"][0]["status"], "failed")

    def test_receiver_must_print_final_stats(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(run.Failure, "without final stats"):
                with self.suite.case("missing-final-stats"):
                    receiver = run.Receiver(self.suite, "receiver", "modern", "none")
                    self.control(receiver, "omit-final")
        self.assertEqual(self.suite.report["cases"][0]["status"], "failed")


if __name__ == "__main__":
    unittest.main(verbosity=2)
