import SwiftUI
import BackgroundTasks
import UIKit
import Mobile

@main
struct AIIOSApp: App {
    @Environment(\.scenePhase) private var phase
    @StateObject private var runtime = RuntimeHolder()

    init() {
        TimeWakeScheduler.shared.register()
    }

    var body: some Scene {
        WindowGroup {
            ShellView(runtime: runtime)
                .ignoresSafeArea()
                .onAppear { runtime.setForeground(phase == .active); runtime.startIfNeeded() }
        }
        .onChange(of: phase) { p in
            runtime.setForeground(p == .active)
        }
    }
}

final class RuntimeHolder: ObservableObject {
    @Published var state = RuntimeState()
    var url: URL? { state.runtime.flatMap { URL(string: $0.dashboardURL()) } }
    var startError: String? { state.error }
    var rt: MobileRuntime? { state.runtime }
    private var started = false

    func startIfNeeded() {
        guard !started else { return }
        started = true
        TimeWakeScheduler.shared.observe(self)
        TimeWakeScheduler.shared.ensureStarted()
    }
    func retry() { TimeWakeScheduler.shared.retry() }
    func setForeground(_ live: Bool) { TimeWakeScheduler.shared.setForeground(live) }
}

final class RuntimeState {
    let runtime: MobileRuntime?
    let error: String?
    let retry: Bool
    init(runtime: MobileRuntime? = nil, error: String? = nil, retry: Bool = false) {
        self.runtime = runtime; self.error = error; self.retry = retry
    }
}

final class TimeWakeScheduler: NSObject, MobileWakeSchedulerProtocol {
    static let shared = TimeWakeScheduler()

    static let taskID = "id.aiii.shell.timewake"
    static let gripTaskID = "id.aiii.shell.grip"

    private let lock = NSLock()
    private let runtimeQueue = DispatchQueue(label: "id.aiii.shell.runtime")
    private var state = RuntimeState()
    private var retryFrom: MobileRuntime?
    private weak var observer: RuntimeHolder?
    private var foreground = false
    private var target: Date?

    func register() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.taskID, using: nil) { task in
            self.handle(task as! BGAppRefreshTask)
        }
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.gripTaskID, using: nil) { task in
            self.handleWake(task, resubmitRefresh: false)
        }
    }

    func submitGrip() {
        let req = BGProcessingTaskRequest(identifier: Self.gripTaskID)
        req.requiresNetworkConnectivity = true
        req.requiresExternalPower = false
        do { try BGTaskScheduler.shared.submit(req) } catch {
            print("AIIOS: grip task submit failed: \(error)")
        }
    }

    private func attach(_ runtime: MobileRuntime) {
        runtime.setWakeScheduler(self)
        runtime.setForegroundNeedListener(IOSForegroundHold.shared)
        publish(RuntimeState(runtime: runtime))
        DispatchQueue.global().async {
            if runtime.waitForRestart() {
                self.runtimeQueue.async {
                    guard self.snapshot().runtime === runtime else { return }
                    self.retryFrom = runtime
                    self.publish(RuntimeState())
                    self.restart(runtime)
                }
            }
        }
    }

    func attachedRuntime() -> MobileRuntime? {
        snapshot().runtime
    }

    private func snapshot() -> RuntimeState {
        lock.lock(); defer { lock.unlock() }
        return state
    }

    func observe(_ holder: RuntimeHolder) {
        observer = holder
        holder.state = snapshot()
    }

    func setForeground(_ live: Bool) {
        foreground = live
        snapshot().runtime?.setForeground(live)
    }

    private func publish(_ next: RuntimeState) {
        lock.lock(); state = next; lock.unlock()
        DispatchQueue.main.async {
            guard self.snapshot() === next else { return }
            next.runtime?.setForeground(self.foreground)
            self.observer?.state = next
        }
    }

    func ensureStarted() { runtimeQueue.async { _ = self.startIfAbsent() } }

    private func startIfAbsent() -> MobileRuntime? {
        let current = snapshot()
        if current.runtime != nil || (current.error != nil && retryFrom != nil) { return current.runtime }
        let home = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true)[0]
        var error: NSError?
        if let runtime = MobileStart(home + "/config.json", home, &error) {
            attach(runtime)
            return runtime
        }
        publish(RuntimeState(error: error?.localizedDescription ?? "The runtime did not start and gave no reason."))
        return nil
    }

    private func restart(_ previous: MobileRuntime) {
        do {
            let runtime = try previous.restart()
            retryFrom = nil
            attach(runtime)
        } catch {
            publish(RuntimeState(error: error.localizedDescription,
                                 retry: previous.canRetryRestart()))
        }
    }

    func retry() {
        runtimeQueue.async {
            guard self.snapshot().retry, let previous = self.retryFrom else { return }
            self.publish(RuntimeState())
            self.restart(previous)
        }
    }

    func schedule(_ atUnixMs: Int64) {
        let at = Date(timeIntervalSince1970: TimeInterval(atUnixMs) / 1000.0)
        lock.lock(); target = at; lock.unlock()
        submit(earliest: at)
    }

    func cancel() {
        lock.lock(); target = nil; lock.unlock()
        BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID)
    }

    private func submit(earliest: Date?) {
        let req = BGAppRefreshTaskRequest(identifier: Self.taskID)
        req.earliestBeginDate = earliest
        do {
            try BGTaskScheduler.shared.submit(req)
        } catch {
            print("AIIOS: BGTask submit failed: \(error)")
        }
    }

    private func handle(_ task: BGAppRefreshTask) {
        handleWake(task, resubmitRefresh: true)
    }

    private func handleWake(_ task: BGTask, resubmitRefresh: Bool) {
        let done = NSLock()
        var completed = false
        let complete: (Bool) -> Void = { ok in
            done.lock(); defer { done.unlock() }
            if !completed { completed = true; task.setTaskCompleted(success: ok) }
        }
        task.expirationHandler = { complete(false) }
        runtimeQueue.async {
            let runtime = self.startIfAbsent()
            self.lock.lock(); let t = self.target; self.lock.unlock()
            runtime?.timeWake()
            if resubmitRefresh {
                if let t = t, t > Date() {
                    self.submit(earliest: t)
                } else {
                    self.submit(earliest: nil)
                }
            }
            complete(runtime != nil)
        }
    }
}

final class IOSForegroundHold: NSObject, MobileForegroundNeedListenerProtocol {
    static let shared = IOSForegroundHold()
    private var task: UIBackgroundTaskIdentifier = .invalid
    private var stopWork: DispatchWorkItem?

    func need(_ active: Bool, reason: String?) {
        DispatchQueue.main.async {
            self.stopWork?.cancel()
            self.stopWork = nil
            if active {
                guard self.task == .invalid else { return }
                self.task = UIApplication.shared.beginBackgroundTask(withName: "aii-grip") {
                    if self.task != .invalid {
                        UIApplication.shared.endBackgroundTask(self.task)
                        self.task = .invalid
                    }
                }
                if UIApplication.shared.applicationState != .active {
                    TimeWakeScheduler.shared.submitGrip()
                }
            } else {
                let w = DispatchWorkItem {
                    if self.task != .invalid {
                        UIApplication.shared.endBackgroundTask(self.task)
                        self.task = .invalid
                    }
                }
                self.stopWork = w
                DispatchQueue.main.asyncAfter(deadline: .now() + 5, execute: w)
            }
        }
    }
}
