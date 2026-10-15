#!/usr/bin/env python3
"""Offline decoder contract tests; no CLI, config, credentials or external tools."""
import copy
import unittest

import verify


class FileDependencies(unittest.TestCase):
    def setUp(self):
        self.record = {"file": {"pieces": [
            {"object": "v3/blobs/zstd/first.zst", "encoded_sha256": "encoded1", "encoded_size": 40,
             "sha256": "raw1", "start": 0, "end": 80},
            {"object": "v3/blobs/zstd/second.zst", "encoded_sha256": "encoded2", "encoded_size": 50,
             "sha256": "raw2", "start": 80, "end": 180}]}}
        self.expected = [
            {"object": "v3/blobs/zstd/first.zst", "sha256": "encoded1", "size": 40,
             "encoding": "zstd", "raw_sha256": "raw1", "raw_size": 80},
            {"object": "v3/blobs/zstd/second.zst", "sha256": "encoded2", "size": 50,
             "encoding": "zstd", "raw_sha256": "raw2", "raw_size": 100}]

    def test_derived_and_legacy(self):
        self.assertEqual(verify.file_dependencies(self.record), self.expected)
        self.record["dependencies"] = copy.deepcopy(self.expected)
        self.assertEqual(verify.file_dependencies(self.record), self.expected)

    def test_explicit_corruption_rejected(self):
        for field in self.expected[0]:
            with self.subTest(field=field):
                record = copy.deepcopy(self.record)
                record["dependencies"] = copy.deepcopy(self.expected)
                record["dependencies"][0][field] = "corrupt"
                with self.assertRaises(RuntimeError):
                    verify.file_dependencies(record)
        for dependencies in (None, [], self.expected[:1], self.expected[::-1], self.expected * 2):
            with self.subTest(dependencies=dependencies):
                self.record["dependencies"] = dependencies
                with self.assertRaises(RuntimeError):
                    verify.file_dependencies(self.record)

    def test_repeated_pieces_are_not_deduplicated(self):
        self.record["file"]["pieces"].append(copy.deepcopy(self.record["file"]["pieces"][0]))
        self.record["file"]["pieces"][-1].update(start=180, end=260)
        self.expected.append(copy.deepcopy(self.expected[0]))
        self.record["dependencies"] = self.expected
        self.assertEqual(verify.file_dependencies(self.record), self.expected)


if __name__ == "__main__":
    unittest.main()
