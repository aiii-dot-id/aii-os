import CryptoKit
import Foundation
import Mobile
import Security

final class DeviceKeystore: NSObject, MobileDeviceKeyProtocol {
    static let shared = DeviceKeystore()

    private static let label = "id.aiii.shell.identity-key-wrap"
    private static let tag = Data(label.utf8)
    private static let enclave: UInt8 = 1
    private static let keychain: UInt8 = 2
    private static let algorithm = SecKeyAlgorithm.eciesEncryptionCofactorVariableIVX963SHA256AESGCM
    private static let aad = Data("AII OS identity key, v1".utf8)

    private let lock = NSLock()
    private var held = "iOS Keychain"

    func wrap(_ plain: Data?) throws -> Data {
        guard let plain = plain else { throw Self.failure("nothing was given to wrap") }
        lock.lock(); defer { lock.unlock() }
        if SecureEnclave.isAvailable {
            let key = try enclaveKey(create: true)
            guard let pub = SecKeyCopyPublicKey(key) else { throw Self.failure("the Secure Enclave key has no public half") }
            var cf: Unmanaged<CFError>?
            guard let sealed = SecKeyCreateEncryptedData(pub, Self.algorithm, plain as CFData, &cf) as Data? else {
                throw Self.error(cf)
            }
            held = "Secure Enclave, this iPhone only, while unlocked"
            return Data([Self.enclave]) + sealed
        }
        let key = try keychainKey(create: true)
        let box = try AES.GCM.seal(plain, using: key, authenticating: Self.aad)
        guard let combined = box.combined else { throw Self.failure("the sealed key could not be put together") }
        held = "iOS Keychain, this iPhone only, while unlocked (no Secure Enclave here)"
        return Data([Self.keychain]) + combined
    }

    func unwrap(_ sealed: Data?) throws -> Data {
        guard let sealed = sealed, let scheme = sealed.first else { throw Self.failure("that is not a key this phone wrapped") }
        let body = sealed.dropFirst()
        lock.lock(); defer { lock.unlock() }
        switch scheme {
        case Self.enclave:
            let key = try enclaveKey(create: false)
            var cf: Unmanaged<CFError>?
            guard let plain = SecKeyCreateDecryptedData(key, Self.algorithm, Data(body) as CFData, &cf) as Data? else {
                throw Self.error(cf)
            }
            held = "Secure Enclave, this iPhone only, while unlocked"
            return plain
        case Self.keychain:
            let key = try keychainKey(create: false)
            let plain = try AES.GCM.open(AES.GCM.SealedBox(combined: body), using: key, authenticating: Self.aad)
            held = "iOS Keychain, this iPhone only, while unlocked (no Secure Enclave here)"
            return plain
        default:
            throw Self.failure("that is not a key this phone wrapped")
        }
    }

    func holder() -> String {
        lock.lock(); defer { lock.unlock() }
        return held
    }

    private func enclaveKey(create: Bool) throws -> SecKey {
        let query: [String: Any] = [
            kSecClass as String: kSecClassKey,
            kSecAttrApplicationTag as String: Self.tag,
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecReturnRef as String: true,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecSuccess, let item = item { return item as! SecKey }
        if status != errSecItemNotFound { throw Self.error(status) }
        guard create else { throw Self.absent() }
        var cf: Unmanaged<CFError>?
        guard let access = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly, .privateKeyUsage, &cf) else {
            throw Self.error(cf)
        }
        let attributes: [String: Any] = [
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits as String: 256,
            kSecAttrTokenID as String: kSecAttrTokenIDSecureEnclave,
            kSecPrivateKeyAttrs as String: [
                kSecAttrIsPermanent as String: true,
                kSecAttrApplicationTag as String: Self.tag,
                kSecAttrAccessControl as String: access,
            ] as [String: Any],
        ]
        guard let key = SecKeyCreateRandomKey(attributes as CFDictionary, &cf) else { throw Self.error(cf) }
        return key
    }

    private func keychainKey(create: Bool) throws -> SymmetricKey {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.label,
            kSecAttrAccount as String: Self.label,
            kSecReturnData as String: true,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecSuccess, let data = item as? Data { return SymmetricKey(data: data) }
        if status != errSecItemNotFound { throw Self.error(status) }
        guard create else { throw Self.absent() }
        let key = SymmetricKey(size: .bits256)
        let add: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.label,
            kSecAttrAccount as String: Self.label,
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
            kSecValueData as String: key.withUnsafeBytes { Data($0) },
        ]
        let added = SecItemAdd(add as CFDictionary, nil)
        guard added == errSecSuccess else { throw Self.error(added) }
        return key
    }

    private static func absent() -> NSError {
        failure("This iPhone's keystore holds no key for the identity's key: the key file was wrapped on another phone, or this phone's keystore was reset. Nothing was changed: the key file and the record are as they were.")
    }

    private static func error(_ status: OSStatus) -> NSError {
        if status == errSecInteractionNotAllowed { return locked() }
        let said = SecCopyErrorMessageString(status, nil) as String? ?? "status \(status)"
        return failure("the keystore answered: \(said)")
    }

    private static func error(_ cf: Unmanaged<CFError>?) -> NSError {
        guard let e = cf?.takeRetainedValue() as Error? as NSError? else { return failure("the keystore gave no reason") }
        if e.domain == NSOSStatusErrorDomain && e.code == Int(errSecInteractionNotAllowed) { return locked() }
        return failure("the keystore answered: \(e.localizedDescription)")
    }

    private static func locked() -> NSError {
        failure("This iPhone is locked, and the identity's key is kept for while it is unlocked. The identity starts when the app is next opened.")
    }

    private static func failure(_ words: String) -> NSError {
        NSError(domain: "id.aiii.shell.keystore", code: 1, userInfo: [NSLocalizedDescriptionKey: words])
    }
}
