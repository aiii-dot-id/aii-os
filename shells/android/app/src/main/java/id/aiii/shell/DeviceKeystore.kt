package id.aiii.shell

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyProperties
import java.security.KeyStore
import java.security.ProviderException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec

object DeviceKeystore : mobile.DeviceKey {
    private const val ALIAS = "id.aiii.shell.identity-key-wrap"
    private const val PROVIDER = "AndroidKeyStore"
    private const val IV_BYTES = 12
    private const val TAG_BITS = 128
    private val AAD = "AII OS identity key, v1".toByteArray()
    @Volatile private var held = "Android Keystore"

    override fun wrap(plain: ByteArray?): ByteArray {
        requireNotNull(plain) { "nothing was given to wrap" }
        synchronized(this) {
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.ENCRYPT_MODE, key(create = true))
            c.updateAAD(AAD)
            val sealed = c.doFinal(plain)
            return c.iv + sealed
        }
    }

    override fun unwrap(sealed: ByteArray?): ByteArray {
        require(sealed != null && sealed.size > IV_BYTES) { "that is not a key this phone wrapped" }
        synchronized(this) {
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.DECRYPT_MODE, key(create = false), GCMParameterSpec(TAG_BITS, sealed, 0, IV_BYTES))
            c.updateAAD(AAD)
            return c.doFinal(sealed, IV_BYTES, sealed.size - IV_BYTES)
        }
    }

    override fun holder(): String = held

    private fun key(create: Boolean): SecretKey {
        val ks = KeyStore.getInstance(PROVIDER).apply { load(null) }
        (ks.getKey(ALIAS, null) as SecretKey?)?.let { describe(it); return it }
        check(create) {
            "This phone's keystore holds no key for the identity's key: the key file was wrapped on another phone, " +
                "or this phone's keystore was reset. Nothing was changed: the key file and the record are as they were."
        }
        val made = if (Build.VERSION.SDK_INT >= 28) {
            try {
                generate(strongBox = true)
            } catch (e: ProviderException) {
                null
            }
        } else null
        return (made ?: generate(strongBox = false)).also { describe(it) }
    }

    private fun generate(strongBox: Boolean): SecretKey {
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
        if (strongBox && Build.VERSION.SDK_INT >= 28) spec.setIsStrongBoxBacked(true)
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        gen.init(spec.build())
        return gen.generateKey()
    }

    private fun describe(key: SecretKey) {
        held = try {
            val info = SecretKeyFactory.getInstance(key.algorithm, PROVIDER).getKeySpec(key, KeyInfo::class.java) as KeyInfo
            if (Build.VERSION.SDK_INT >= 31) {
                when (info.securityLevel) {
                    KeyProperties.SECURITY_LEVEL_STRONGBOX -> "Android Keystore, StrongBox"
                    KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT -> "Android Keystore, TEE"
                    KeyProperties.SECURITY_LEVEL_SOFTWARE -> "Android Keystore, in software: this phone gave it no secure hardware"
                    else -> "Android Keystore, security level ${info.securityLevel}"
                }
            } else {
                @Suppress("DEPRECATION")
                if (info.isInsideSecureHardware) "Android Keystore, secure hardware"
                else "Android Keystore, in software: this phone gave it no secure hardware"
            }
        } catch (e: Exception) {
            "Android Keystore (where it holds the key could not be read: ${e.message})"
        }
    }
}
