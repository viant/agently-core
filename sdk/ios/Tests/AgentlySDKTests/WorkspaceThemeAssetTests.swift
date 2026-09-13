import XCTest
@testable import AgentlySDK

final class WorkspaceThemeAssetTests: XCTestCase {
    final class ProtocolStub: URLProtocol {
        static var handler: ((URLRequest) throws -> (HTTPURLResponse, Data))?
        override class func canInit(with request: URLRequest) -> Bool { true }
        override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
        override func startLoading() {
            do {
                let (response, data) = try Self.handler!(request)
                client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: data)
                client?.urlProtocolDidFinishLoading(self)
            } catch { client?.urlProtocol(self, didFailWithError: error) }
        }
        override func stopLoading() {}
    }
    func testCatalogRequestUsesConfiguredAuthenticationAndMetadataSession() async throws {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ProtocolStub.self]
        let session = URLSession(configuration: config)
        let revision = String(repeating: "a", count: 64)
        let href = "/v1/workspace/ui/themes/\(revision).json"
        ProtocolStub.handler = { request in
            XCTAssertEqual(request.url?.path, href)
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fixture")
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, Data("{}".utf8))
        }
        defer { ProtocolStub.handler = nil; session.invalidateAndCancel() }
        let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "https://workspace.example")!, headers: ["Authorization": "Bearer fixture"])], session: session, metadataSession: session)
        let data = try await client.getWorkspaceThemeCatalog(WorkspaceAssetDescriptor(revision: revision, href: href))
        XCTAssertEqual(data, Data("{}".utf8))
        do {
            _ = try await client.getWorkspaceThemeCatalog(WorkspaceAssetDescriptor(revision: revision, href: "https://other.example/theme.json"))
            XCTFail("arbitrary URL accepted")
        } catch {}
    }
    func testSparseMetadataPreservesThemeDescriptors() async throws {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ProtocolStub.self]
        let session = URLSession(configuration: config)
        let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "https://workspace.example")!)], session: session)
        let revision = String(repeating: "b", count: 64)
        let data = Data("""
        {"data":{"workspaceId":"workspace","uiThemes":{"version":1,"revision":"\(revision)","href":"/v1/workspace/ui/themes/\(revision).json"}}}
        """.utf8)
        ProtocolStub.handler = { request in
            XCTAssertEqual(request.url?.path, "/v1/workspace/metadata")
            return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, data)
        }
        defer { ProtocolStub.handler = nil; session.invalidateAndCancel() }
        let metadata = try await client.getWorkspaceMetadata()
        XCTAssertEqual(metadata.workspaceId, "workspace")
        XCTAssertEqual(metadata.uiThemes?.revision, revision)
        XCTAssertTrue(metadata.agents.isEmpty)
        XCTAssertTrue(metadata.models.isEmpty)
    }
}
