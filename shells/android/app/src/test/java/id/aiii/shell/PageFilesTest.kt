package id.aiii.shell

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// The page's files (h-mobile-05): only this dashboard's downloads are taken,
// a name is never a path, and a blob arrives whole or not at all.
class PageFilesTest {
    private val origin = "http://127.0.0.1:8180"

    @Test fun onlyTheDashboardsOwnDownloadsAreTaken() {
        assertEquals(PageFiles.Kind.BLOB, PageFiles.kindOf("blob:$origin/0b6c-4f", origin))
        assertEquals(PageFiles.Kind.PAGE, PageFiles.kindOf("$origin/database/export", origin))
        assertEquals(PageFiles.Kind.PAGE, PageFiles.kindOf("$origin/w?p=notes.txt&download=1", origin))
        assertEquals(PageFiles.Kind.OTHER, PageFiles.kindOf("http://127.0.0.1:81800/x", origin))
        assertEquals(PageFiles.Kind.OTHER, PageFiles.kindOf("blob:https://example.test/0b6c", origin))
        assertEquals(PageFiles.Kind.OTHER, PageFiles.kindOf("https://example.test/file", origin))
    }

    @Test fun aNameIsNeverAPath() {
        assertEquals("escrow-20261021.age", PageFiles.safeName("escrow-20261021.age"))
        assertEquals("identity.sec", PageFiles.safeName("../../data/identity.sec"))
        assertEquals("x.txt", PageFiles.safeName("C:\\a\\x.txt"))
        assertEquals("download", PageFiles.safeName(".."))
        assertEquals("download", PageFiles.safeName(""))
        assertEquals("ab", PageFiles.safeName("a\u0000b"))
    }

    @Test fun aBlobArrivesWholeOrNotAtAll() {
        val got = PageFiles.blobMessage("escrow.age\ndata:application/octet-stream;base64,AAEC+g==")!!
        assertEquals("escrow.age", got.first)
        assertArrayEquals(byteArrayOf(0, 1, 2, 0xfa.toByte()), got.second)
        assertNull(PageFiles.blobMessage("escrow.age"))
        assertNull(PageFiles.blobMessage("\ndata:;base64,AA=="))
        assertNull(PageFiles.blobMessage("x\ndata:text/plain,hello"))
        assertNull(PageFiles.blobMessage("x\ndata:;base64,%%%"))
    }
}
