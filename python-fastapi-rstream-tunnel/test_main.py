from contextlib import asynccontextmanager
import asyncio
import unittest
from unittest.mock import AsyncMock, MagicMock, patch

from fastapi import FastAPI
import main
import rstream


class ApplicationLifecycleTest(unittest.IsolatedAsyncioTestCase):
    async def test_application_owns_startup_and_shutdown_on_all_exit_paths(self):
        for failure in ("none", "connect", "serve", "cancel"):
            with self.subTest(failure=failure):
                await self.check_exit_path(failure)

    async def check_exit_path(self, failure):
        events = []

        @asynccontextmanager
        async def lifespan(app):
            app.state.ready = True
            events.append("startup")
            try:
                yield
            finally:
                app.state.ready = False
                events.append("shutdown")

        app = FastAPI(lifespan=lifespan)
        control = MagicMock()
        control.__aenter__ = AsyncMock(return_value=control)
        control.__aexit__ = AsyncMock(return_value=False)
        tunnel = MagicMock(forwarding_address="https://example.test")
        control.create_tunnel = AsyncMock(return_value=tunnel)
        client = MagicMock()
        client.__aenter__ = AsyncMock(return_value=client)
        client.__aexit__ = AsyncMock(return_value=False)

        async def connect():
            self.assertTrue(app.state.ready)
            events.append("connect")
            if failure == "connect":
                raise RuntimeError("synthetic connect failure")
            return control

        async def serve(application, accepted):
            self.assertIs(application, app)
            self.assertIs(accepted, tunnel)
            self.assertTrue(app.state.ready)
            events.append("serve")
            if failure == "serve":
                raise RuntimeError("synthetic serving failure")
            if failure == "cancel":
                raise asyncio.CancelledError

        client.connect = connect
        with (
            patch.object(main, "app", app),
            patch.object(rstream.Client, "from_env", return_value=client),
            patch.object(rstream.asgi, "serve", side_effect=serve),
            patch("sys.argv", ["main.py"]),
        ):
            if failure == "none":
                await main.main()
            else:
                expected = asyncio.CancelledError if failure == "cancel" else RuntimeError
                with self.assertRaises(expected):
                    await main.main()
        self.assertEqual(events, ["startup", "connect"] + ([] if failure == "connect" else ["serve"]) + ["shutdown"])
        self.assertFalse(app.state.ready)
        client.__aexit__.assert_awaited_once()


if __name__ == "__main__":
    unittest.main()
