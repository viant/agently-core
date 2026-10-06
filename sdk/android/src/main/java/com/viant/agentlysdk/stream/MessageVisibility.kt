package com.viant.agentlysdk.stream

internal fun isInternalMessageMode(mode: String?): Boolean =
    mode?.trim()?.lowercase() in setOf("router", "chain")
