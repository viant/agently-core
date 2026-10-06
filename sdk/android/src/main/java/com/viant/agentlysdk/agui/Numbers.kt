package com.viant.agentlysdk.agui

import java.math.BigInteger

/** JSON numerical equality without binary floating-point or exponent overflow. */
internal object AgUiNumbers {
    private val syntax = Regex("-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?")
    fun valid(token: String) = syntax.matches(token)
    private data class Parts(val negative: Boolean, val digits: String, val exponent: BigInteger)
    private fun parts(token: String): Parts {
        val negative = token.startsWith('-'); val unsigned = token.removePrefix("-")
        val split = unsigned.lowercase().split('e'); val decimal = split[0].split('.')
        val raw = decimal.joinToString("").trimStart('0')
        if (raw.isEmpty()) return Parts(false, "0", BigInteger.ZERO)
        val digits = raw.trimEnd('0')
        val exponent = (split.getOrNull(1)?.toBigInteger() ?: BigInteger.ZERO) - (decimal.getOrNull(1)?.length ?: 0).toBigInteger() + (raw.length - digits.length).toBigInteger()
        return Parts(negative, digits, exponent)
    }
    fun integral(token: String): Boolean = parts(token).let { it.digits == "0" || it.exponent.signum() >= 0 }
    fun compare(a: String, b: String): Int {
        val x = parts(a); val y = parts(b)
        if (x.digits == "0" && y.digits == "0") return 0
        if (x.negative != y.negative) return if (x.negative) -1 else 1
        if (x.digits == "0") return if (y.negative) 1 else -1
        if (y.digits == "0") return if (x.negative) -1 else 1
        val magnitude = (x.exponent + x.digits.length.toBigInteger()).compareTo(y.exponent + y.digits.length.toBigInteger())
        val result = if (magnitude != 0) magnitude else {
            val size = maxOf(x.digits.length, y.digits.length)
            x.digits.padEnd(size, '0').compareTo(y.digits.padEnd(size, '0'))
        }
        return if (x.negative) -result else result
    }
}
