package id.aiii.shell

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RuntimeStartTest {
    @Test fun aWakeLeaseDoesNotJoinHandoffButStillStartsAColdRuntime() {
        assertFalse(joinsRuntimeStart(wake = true, handoff = true))
        assertTrue(joinsRuntimeStart(wake = true, handoff = false))
    }

    @Test fun aWindowAwaitsBothKindsOfStartup() {
        assertTrue(joinsRuntimeStart(wake = false, handoff = true))
        assertTrue(joinsRuntimeStart(wake = false, handoff = false))
    }
}
