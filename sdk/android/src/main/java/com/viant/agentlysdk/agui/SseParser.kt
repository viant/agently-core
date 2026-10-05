package com.viant.agentlysdk.agui

import java.io.ByteArrayOutputStream

/** Incremental WHATWG SSE framing across byte/UTF-8/CR/LF boundaries. */
class AgUiSSEParser(private val maxFrameBytes: Int = 64 * 1024 * 1024) {
    private var line = ByteArrayOutputStream()
    private val dataLines = mutableListOf<String>()
    private var previousCR = false
    private var firstLine = true
    private var frameBytes = 0
    fun feed(bytes: ByteArray): List<String> {
        val frames = mutableListOf<String>()
        for (byte in bytes) {
            when (byte.toInt() and 255) {
                13 -> { consumeLine(frames); previousCR = true }
                10 -> { if (!previousCR) consumeLine(frames); previousCR = false }
                else -> { previousCR = false; line.write(byte.toInt()); if (line.size() + frameBytes > maxFrameBytes) fail("SSE frame exceeds configured limit") }
            }
        }
        return frames
    }
    private fun consumeLine(frames: MutableList<String>) {
        val bytes = line.toByteArray()
        // A large tool-result frame must not leave its growing buffer resident for
        // the remainder of the stream. ByteArray.drop also boxes every byte;
        // decode the data slice directly instead of constructing byte lists.
        if (line.size() > 64 * 1024) line = ByteArrayOutputStream() else line.reset()
        var start = 0
        if (firstLine) {
            firstLine = false
            if (bytes.size >= 3 && bytes[0] == 0xEF.toByte() && bytes[1] == 0xBB.toByte() && bytes[2] == 0xBF.toByte()) start = 3
        }
        if (start == bytes.size) {
            if (dataLines.isNotEmpty()) frames += if (dataLines.size == 1) dataLines[0] else dataLines.joinToString("\n")
            dataLines.clear(); frameBytes = 0
        } else if (bytes.size - start >= 5 && bytes[start] == 'd'.code.toByte() && bytes[start + 1] == 'a'.code.toByte() && bytes[start + 2] == 't'.code.toByte() && bytes[start + 3] == 'a'.code.toByte() && bytes[start + 4] == ':'.code.toByte()) {
            var dataStart = start + 5
            if (dataStart < bytes.size && bytes[dataStart] == 32.toByte()) dataStart++
            val size = bytes.size - dataStart
            frameBytes += size + 1
            if (frameBytes > maxFrameBytes) fail("SSE frame exceeds configured limit")
            dataLines += String(bytes, dataStart, size, Charsets.UTF_8)
        }
    }
    /** EOF discards an event without its terminating blank line. */
    fun finish() { line = ByteArrayOutputStream(); dataLines.clear(); frameBytes = 0 }
}
