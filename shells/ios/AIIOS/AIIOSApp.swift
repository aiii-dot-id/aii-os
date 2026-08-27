// The iOS shell (MOBILE_PORT §8): the runtime is the identity; this app
// is a window and a wake source. Mirrors the Android shell: start off
// the main thread, WebView on the local dashboard, foreground=presence.
import SwiftUI
import BackgroundTasks
import UIKit
import Mobile

@main
struct AIIOSApp: App {
    @Environment(\.scenePhase) private var phase
    @StateObject private var runtime = RuntimeHolder()

    init() {
        // BGTaskScheduler's law: handlers register before the app
        // finishes launching, and only once. App.init is SwiftUI's
        // moment for it — the handler body waits for attach().
        TimeWakeScheduler.shared.register()
    }

    var body: some Scene {
        WindowGroup {
            ShellView(runtime: runtime)
                .ignoresSafeArea()
                .onAppear { runtime.startIfNeeded() }
        }
        .onChange(of: phase) { p in
            runtime.rt?.setForeground(p == .active)
        }
    }
}

final class RuntimeHolder: ObservableObject {
    @Published var url: URL?
    // A failed Start used to be DISCARDED: err was passed in and never
    // read, so the shell showed an empty WebView on its own background
    // colour and the operator had a black rectangle and no reason for
    // it. Observed 2026-08-24 on the simulator, where a stale container
    // path made Start fail and nothing said so anywhere.
    @Published var startError: String?
    var rt: MobileRuntime?
    private var started = false

    func startIfNeeded() {
        guard !started else { return }
        started = true
        // ADOPT a runtime the background path already started (a BGTask
        // cold-started the process, then the operator opened the app):
        // the runtime is a process singleton — a second Start would die
        // on the port (the Android shell's 2026-08-18 lesson, mirrored).
        if let existing = TimeWakeScheduler.shared.attachedRuntime() {
            self.rt = existing
            self.url = URL(string: existing.dashboardURL())
            return
        }
        let home = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true)[0]
        DispatchQueue.global().async {
            var err: NSError?
            let rt = MobileStart(home + "/config.json", home, &err)
            DispatchQueue.main.async {
                self.rt = rt
                if rt == nil {
                    self.startError = err?.localizedDescription ?? "the runtime did not start and gave no reason"
                }
                if let rt = rt {
                    self.url = URL(string: rt.dashboardURL())
                    // The OUTBOUND wake seam, wired right after Start
                    // (MOBILE_PORT §2): from here on TIME drives
                    // schedule()/cancel() with its next-due moment, and
                    // the BGTask handler feeds wakes back into timeWake.
                    TimeWakeScheduler.shared.attach(rt)
                }
            }
        }
    }
}

// TimeWakeScheduler is the Swift half of the OS-wake loop (MOBILE_PORT
// §2): Go's TIME hands over its ONE next-due moment (each schedule
// supersedes the last — same-identifier submits replace, so the
// one-slot contract comes free from the platform), and the
// BGAppRefreshTask handler hands the wake back to timeWake(), whose
// catch-up fires everything due.
//
// PLATFORM HONESTY: BGTaskScheduler gives no exactness by design.
// earliestBeginDate is "not before", never "at" — iOS decides when,
// from its own budget heuristics, or not at all. Android with the
// exact-alarm grant is near-exact; iOS wakes are OPPORTUNISTIC. TIME
// tolerates this by construction: a late wake evaluates late — alarms
// fire late, never lost (the durable row + catch-up is the correctness
// floor; the OS wake is acceleration, canon #13).
final class TimeWakeScheduler: NSObject, MobileWakeSchedulerProtocol {
    static let shared = TimeWakeScheduler()

    // ONE static identifier — BGTaskSchedulerPermittedIdentifiers is a
    // build-time plist list, per-timer ids are impossible (M21). Must
    // match Info.plist / project.yml exactly.
    static let taskID = "id.aiii.shell.timewake"
    // The grip task: minutes-scale opportunistic continuation while the
    // Go foreground registry holds work (BGProcessingTask; Sev batch 2:
    // extended execution is ~30s, processing may run for minutes — both
    // remain interruptible, and TIME's catch-up stays the floor).
    static let gripTaskID = "id.aiii.shell.grip"

    private let lock = NSLock()
    private let startLock = NSLock()
    private var rt: MobileRuntime?
    private var target: Date? // the last next-due TIME asked for

    func register() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.taskID, using: nil) { task in
            self.handle(task as! BGAppRefreshTask)
        }
        // The grip task shares the wake body: cold-start if needed,
        // evaluate, complete honestly. iOS grants it minutes, not a
        // promise — the expiration handler completes early and the
        // durable rows carry whatever was cut.
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.gripTaskID, using: nil) { task in
            self.handleWake(task, resubmitRefresh: false)
        }
    }

    // submitGrip asks for a processing window while work is held and
    // the app is backgrounded. Network required: held work is a turn
    // or a claimed item, and both speak to a model.
    func submitGrip() {
        let req = BGProcessingTaskRequest(identifier: Self.gripTaskID)
        req.requiresNetworkConnectivity = true
        req.requiresExternalPower = false
        do { try BGTaskScheduler.shared.submit(req) } catch {
            print("AIIOS: grip task submit failed: \(error)")
        }
    }

    func attach(_ runtime: MobileRuntime) {
        lock.lock(); rt = runtime; lock.unlock()
        runtime.setWakeScheduler(self)
        // The foreground-need seam rides the same attach so BOTH start
        // paths (UI launch, background cold-start) wire it.
        runtime.setForegroundNeedListener(IOSForegroundHold.shared)
    }

    // attachedRuntime lets the UI adopt a runtime the background path
    // started — one process, one runtime, whichever door opened first.
    func attachedRuntime() -> MobileRuntime? {
        lock.lock(); defer { lock.unlock() }
        return rt
    }

    // --- MobileWakeSchedulerProtocol ---
    // Called from Go's scheduler goroutine, never the main thread;
    // BGTaskScheduler is thread-safe, so no hopping.

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
            // The simulator has no BGTaskScheduler; on device a failure
            // costs only opportunism — the foreground catch-up remains
            // the correctness floor.
            print("AIIOS: BGTask submit failed: \(error)")
        }
    }

    // The OS invited us back. TimeWake evaluates everything due — it
    // works while quiesced, the invited path — then we RE-REQUEST:
    // while backgrounded the Go scheduler is parked and re-arms the
    // slot only on foreground resume, so the shell keeps the chain
    // alive. If TIME's last target is still ahead, ask for it again;
    // otherwise ask with no earliest date — whenever iOS finds budget —
    // so catch-up keeps getting opportunistic chances until the
    // operator returns.
    private func handle(_ task: BGAppRefreshTask) {
        handleWake(task, resubmitRefresh: true)
    }

    // handleWake is both background entries (refresh and grip): honor
    // expiration by completing early and exactly once — iOS tunes the
    // budget from completion honesty, and a handler that keeps running
    // past expiration is killed (Sev batch 2: "honor expiration").
    // Mid-dispatch kill stays safe (M16): TIME's transitions commit
    // after owner completion; an expired task only means the next wake
    // repeats the catch-up.
    private func handleWake(_ task: BGTask, resubmitRefresh: Bool) {
        let done = NSLock()
        var completed = false
        let complete: (Bool) -> Void = { ok in
            done.lock(); defer { done.unlock() }
            if !completed { completed = true; task.setTaskCompleted(success: ok) }
        }
        task.expirationHandler = { complete(false) }
        // Serialize cold-start: refresh and grip can be launched for
        // the same dead process; two MobileStarts would race the port
        // (Method review). startLock held across Start is fine here —
        // handlers run on background queues.
        startLock.lock(); defer { startLock.unlock() }
        lock.lock(); var runtime = rt; let t = target; lock.unlock()
        // COLD START IS THE POINT (Sol P1-2): when iOS launched a dead
        // process for this task, the wake itself is the reason the
        // runtime must start — "a wake before Start is meaningless" was
        // exactly backwards for a process the wake created. Same paths
        // as the UI start; attach() wires both seams.
        if runtime == nil {
            let home = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true)[0]
            var err: NSError?
            if let started = MobileStart(home + "/config.json", home, &err) {
                attach(started)
                runtime = started
            } else {
                print("AIIOS: background cold-start failed: \(err?.localizedDescription ?? "no reason")")
            }
        }
        runtime?.timeWake()
        if resubmitRefresh {
            if let t = t, t > Date() {
                submit(earliest: t)
            } else {
                submit(earliest: nil)
            }
        }
        // Honest budget accounting: success only when a runtime
        // actually evaluated the wake — iOS tunes future budget from
        // this, and claiming success for a no-op starves real wakes.
        complete(runtime != nil)
    }
}

// The iOS half of internal/foreground: while a grip is held (a turn in
// flight, a claimed work item) and the app is NOT active, ask UIKit for
// extended background execution — the ~30s the platform grants, renewed
// per grip; the BGTask chain remains the long-horizon path. The false
// edge is debounced 5s per mobile.go's listener doc (turns chain
// quickly). Foregrounded, the hold is a no-op by construction: active
// apps are not suspended.
final class IOSForegroundHold: NSObject, MobileForegroundNeedListenerProtocol {
    static let shared = IOSForegroundHold()
    private var task: UIBackgroundTaskIdentifier = .invalid
    private var stopWork: DispatchWorkItem?

    func need(_ active: Bool, reason: String) {
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
                // Beyond the ~30s of extended execution: ask for a
                // processing window too when the app is not active —
                // minutes-scale, iOS-scheduled, still interruptible.
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
