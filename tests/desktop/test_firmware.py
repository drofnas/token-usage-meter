"""Verify immutable firmware without rejecting the device's updated OTA boot state."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('firmware_helper', ROOT / 'scripts/firmware.py')
firmware = importlib.util.module_from_spec(spec)
spec.loader.exec_module(firmware)


class FirmwareCommands(unittest.TestCase):
    def test_boot_state_is_flashed_but_not_compared_after_boot(self):
        with tempfile.TemporaryDirectory() as directory:
            build = Path(directory)
            files = {'0x1000': 'bootloader.bin', '0x8000': 'partitions.bin',
                     '0x9000': 'ota_data_initial.bin', '0x10000': 'application.bin'}
            (build / 'flasher_args.json').write_text(json.dumps({'flash_files': files}))
            for name in files.values():
                (build / name).write_bytes(b'synthetic firmware')
            for command in ('flash', 'verify'):
                with self.subTest(command=command), patch.object(firmware, 'BUILD', build), \
                        patch.object(firmware, 'python_path', return_value='python'), \
                        patch.object(firmware, 'run') as run, \
                        patch('sys.argv', ['firmware.py', command, '--port', '/test/cyd']):
                    firmware.main()
                    argv = run.call_args.args
                    self.assertIn('write-flash' if command == 'flash' else 'verify-flash', argv)
                    for name in ('bootloader.bin', 'partitions.bin', 'application.bin'):
                        self.assertIn(build / name, argv)
                    self.assertEqual(build / 'ota_data_initial.bin' in argv, command == 'flash')


if __name__ == '__main__':
    unittest.main()
