import Metal
import Foundation
// A compute kernel compiled at run time and run on the GPU: the shape of
// an inference engine's first step, inside the host's containment.
guard let dev = MTLCreateSystemDefaultDevice() else { print("NO METAL DEVICE"); exit(2) }
let src = """
#include <metal_stdlib>
using namespace metal;
kernel void square(device const float* a [[buffer(0)]], device float* b [[buffer(1)]], uint i [[thread_position_in_grid]]) { b[i] = a[i] * a[i]; }
"""
do {
  let lib = try dev.makeLibrary(source: src, options: nil)
  let fn = lib.makeFunction(name: "square")!
  let pso = try dev.makeComputePipelineState(function: fn)
  let n = 1 << 20
  var a = [Float](repeating: 0, count: n); for i in 0..<n { a[i] = Float(i % 100) }
  let ba = dev.makeBuffer(bytes: a, length: n*4)!, bb = dev.makeBuffer(length: n*4)!
  let q = dev.makeCommandQueue()!, cb = q.makeCommandBuffer()!, enc = cb.makeComputeCommandEncoder()!
  enc.setComputePipelineState(pso); enc.setBuffer(ba, offset: 0, index: 0); enc.setBuffer(bb, offset: 0, index: 1)
  enc.dispatchThreads(MTLSize(width: n, height: 1, depth: 1), threadsPerThreadgroup: MTLSize(width: 256, height: 1, depth: 1))
  enc.endEncoding(); cb.commit(); cb.waitUntilCompleted()
  let out = bb.contents().bindMemory(to: Float.self, capacity: n)
  print("GPU \(dev.name): \(n) squares, out[7]=\(out[7]) out[99]=\(out[99]) status=\(cb.status.rawValue)")
} catch { print("METAL FAILED: \(error)"); exit(3) }
