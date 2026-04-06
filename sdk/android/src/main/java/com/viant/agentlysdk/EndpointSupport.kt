package com.viant.agentlysdk

import com.viant.forgeandroid.runtime.EndpointConfig
import okhttp3.Request

internal fun Request.Builder.applyEndpointConfig(config: EndpointConfig): Request.Builder {
    config.defaultHeadersProvider?.invoke()?.forEach { (name, value) ->
        if (name.isNotBlank() && value.isNotBlank()) {
            header(name, value)
        }
    }
    config.authTokenProvider?.invoke()?.takeIf { it.isNotBlank() }?.let {
        header("Authorization", "Bearer $it")
    }
    return this
}
