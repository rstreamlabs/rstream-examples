import hashlib
import json
import pathlib
import tempfile
import unittest

from write_manifest import build_manifest, tool_identity


class WriteManifestTests(unittest.TestCase):
    def test_tool_identity_excludes_absolute_path(self) -> None:
        with tempfile.TemporaryDirectory(prefix="private-owner-") as directory:
            executable = pathlib.Path(directory) / "example-tool"
            executable.write_text("#!/bin/sh\nprintf 'example 1.2.3\\n'\n", encoding="utf-8")
            executable.chmod(0o755)
            identity = tool_identity(str(executable))
            self.assertIsNotNone(identity)
            assert identity is not None
            self.assertEqual(identity["name"], "example-tool")
            self.assertEqual(identity["version"], "example 1.2.3")
            self.assertEqual(
                identity["sha256"], hashlib.sha256(executable.read_bytes()).hexdigest()
            )
            self.assertNotIn(directory, json.dumps(identity))

    def test_manifest_uses_portable_tool_identities(self) -> None:
        manifest = build_manifest(
            "scenario",
            "a" * 40,
            False,
            "",
            "",
            300,
            320,
            240,
        )
        tools = manifest["tools"]
        assert isinstance(tools, dict)
        self.assertIsNone(tools["goNetcat"])
        self.assertIsNone(tools["cppNetcat"])
        self.assertEqual(manifest["git"], {"revision": "a" * 40, "dirty": False})
        self.assertNotIn("rstreamContext", manifest)
        media = manifest["media"]
        assert isinstance(media, dict)
        self.assertEqual(media["frames"], 300)


if __name__ == "__main__":
    unittest.main()
