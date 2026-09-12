import Foundation
import XCTest
@testable import AgentlySDK

final class ResourceUploadTests: XCTestCase {
    final class UploadProtocol: URLProtocol {
        static var handler: ((URLRequest) throws -> (Int, Data))?
        override class func canInit(with request: URLRequest) -> Bool { true }
        override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
        override func startLoading() {
            do {
                let (code, body) = try Self.handler!(request)
                let response = HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil,
                                               headerFields: ["Content-Type": "application/json"])!
                client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: body)
                client?.urlProtocolDidFinishLoading(self)
            } catch { client?.urlProtocol(self, didFailWithError: error) }
        }
        override func stopLoading() {}
    }

    private func makeClient() -> AgentlyClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [UploadProtocol.self]
        return AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "http://upload.test")!,
                             headers: ["Authorization": "Bearer fixture-token"])],
                             session: URLSession(configuration: config))
    }
    private func body(_ request: URLRequest) -> Data {
        if let data = request.httpBody { return data }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open(); defer { stream.close() }
        var data = Data(); var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(contentsOf: buffer.prefix(count))
        }
        return data
    }

    func testUploadThenQueryUsesResourceURI() async throws {
        defer { UploadProtocol.handler = nil }
        for conversationID in ["conv-1", nil] as [String?] {
            let client = makeClient()
            let payload = Data([0x50, 0x4b, 0x00, 0xff, 0x01])
            var requests = 0
            UploadProtocol.handler = { request in
                requests += 1
                XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fixture-token")
                if requests == 1 {
                    XCTAssertEqual(request.url?.path, conversationID == nil ? "/upload" : "/v1/files")
                    XCTAssertTrue(request.value(forHTTPHeaderField: "Content-Type")!.contains("multipart/form-data"))
                    let bytes = self.body(request)
                    XCTAssertNotNil(bytes.range(of: payload))
                    let text = String(decoding: bytes, as: UTF8.self)
                    XCTAssertTrue(text.contains("filename=\"customers.xlsx\""))
                    XCTAssertEqual(text.contains("name=\"conversationId\""), conversationID != nil)
                    return (200, Data(#"{"id":"a1","uri":"scratchpad://artifact/a1","name":"customers.xlsx","size":5,"resource":{"uri":"scratchpad://artifact/a1","id":"a1","name":"customers.xlsx","mimeType":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet","sizeBytes":5,"sha256":"digest"}}"#.utf8))
                }
                XCTAssertEqual(request.url?.path, "/v1/agent/query")
                let json = try XCTUnwrap(JSONSerialization.jsonObject(with: self.body(request)) as? [String: Any])
                XCTAssertEqual(json["resourceURIs"] as? [String], ["scratchpad://artifact/a1"])
                XCTAssertEqual((json["attachments"] as? [Any])?.count, 0)
                return (200, Data(#"{"content":"done"}"#.utf8))
            }
            let upload = try await client.uploadFile(UploadFileInput(conversationID: conversationID,
                name: "customers.xlsx", contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data: payload))
            let resource = try XCTUnwrap(upload.resource)
            XCTAssertEqual(resource.sizeBytes, 5)
            let result = try await client.query(QueryInput(conversationID: "conv-1", query: "Inspect it", resourceURIs: [resource.uri]))
            XCTAssertEqual(result.content, "done")
            XCTAssertEqual(requests, 2)
        }
    }

    func testLegacyAndAnonymousUploadResponses() throws {
        for raw in [#"{"id":"a","uri":"/v1/files/a"}"#, #"{"ID":"a","URI":"/v1/files/a"}"#] {
            let out = try JSONDecoder().decode(UploadFileOutput.self, from: Data(raw.utf8))
            XCTAssertEqual(out.id, "a"); XCTAssertEqual(out.uri, "/v1/files/a"); XCTAssertNil(out.resource)
        }
        let staged = try JSONDecoder().decode(UploadFileOutput.self, from: Data(#"{"uri":"agently-staging-uploads/id/file.bin"}"#.utf8))
        XCTAssertNil(staged.id); XCTAssertNil(staged.resource)
    }

    func testUploadValidationAndHTTPFailure() async throws {
        defer { UploadProtocol.handler = nil }
        let client = makeClient()
        UploadProtocol.handler = { _ in XCTFail("invalid input must not make a request"); return (500, Data()) }
        do { _ = try await client.uploadFile(UploadFileInput(name: "empty", data: Data())); XCTFail("expected validation error") }
        catch { XCTAssertTrue(error is AgentlySDKError) }
        do { _ = try await client.uploadFile(UploadFileInput(name: "x", contentType: "text/plain\r\nX-Injected: yes", data: Data([1]))); XCTFail("expected validation error") }
        catch { XCTAssertTrue(error is AgentlySDKError) }
        UploadProtocol.handler = { _ in (413, Data("too large".utf8)) }
        do { _ = try await client.uploadFile(UploadFileInput(name: "x", data: Data([1]))); XCTFail("expected HTTP failure") }
        catch AgentlySDKError.httpStatus(let status, _) { XCTAssertEqual(status, 413) }
    }

    func testFilenameIsEscapedInMultipartHeader() async throws {
        defer { UploadProtocol.handler = nil }
        UploadProtocol.handler = { request in
            let text = String(decoding: self.body(request), as: UTF8.self)
            XCTAssertTrue(text.contains("filename=\"a%22%0D%0Ab.csv\""))
            return (200, Data(#"{"uri":"scratchpad://artifact/a"}"#.utf8))
        }
        _ = try await makeClient().uploadFile(UploadFileInput(name: "a\"\r\nb.csv", data: Data([1])))
    }
}
