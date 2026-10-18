package id.aiii.shell

import androidx.webkit.WebMessageCompat
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.DataInputStream

class DashboardAuthTest {
    @Test fun theShellRetainsItsJava17BytecodeTarget() {
        DataInputStream(DashboardAuth::class.java.getResourceAsStream("/id/aiii/shell/DashboardAuth.class")).use {
            assertEquals(0xCAFEBABE.toInt(), it.readInt())
            it.readUnsignedShort() // minor
            assertEquals(61, it.readUnsignedShort()) // Java 17
        }
    }

    @Test fun onlyTheMainFramesTokenRequestIsAccepted() {
        assertTrue(DashboardAuth.isTokenRequest(true, WebMessageCompat("token")))
        assertFalse(DashboardAuth.isTokenRequest(false, WebMessageCompat("token")))
        assertFalse(DashboardAuth.isTokenRequest(true, WebMessageCompat("other")))
        assertFalse(DashboardAuth.isTokenRequest(true, WebMessageCompat("")))
    }

    @Test fun binaryMessagesAreRefusedWithoutReadingTheStringAccessor() {
        val binary = WebMessageCompat(byteArrayOf(1, 2, 3))
        assertFalse(DashboardAuth.isTokenRequest(true, binary))
        assertFalse(DashboardAuth.isTokenRequest(false, binary))
    }
}
