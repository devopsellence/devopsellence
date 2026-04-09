# frozen_string_literal: true

require "test_helper"

class StorageObjectStoreTest < ActiveSupport::TestCase
  test "reuses one authorization header across repeated writes" do
    credentials = mock("credentials")
    credentials.expects(:authorization_header).once.returns("Bearer cached-token")
    Gcp::Credentials.expects(:new).once.with(scope: Storage::ObjectStore::SCOPE).returns(credentials)

    stub_request(:post, "https://storage.googleapis.com/upload/storage/v1/b/bucket-a/o?uploadType=media&name=prefix%2Fone.json")
      .with(headers: { "Authorization" => "Bearer cached-token", "Content-Type" => "application/json" })
      .to_return(status: 200, body: "{}")
    stub_request(:post, "https://storage.googleapis.com/upload/storage/v1/b/bucket-a/o?uploadType=media&name=prefix%2Ftwo.json")
      .with(headers: { "Authorization" => "Bearer cached-token", "Content-Type" => "application/json" })
      .to_return(status: 200, body: "{}")

    store = Storage::ObjectStore::GcsStore.new(
      bucket: "bucket-a",
      prefix: "prefix",
      endpoint: "https://storage.googleapis.com"
    )

    store.write_json!(object_path: "one.json", payload: { hello: "world" })
    store.write_json!(object_path: "two.json", payload: { hello: "again" })
  end

  test "batch writes share one authorization header" do
    credentials = mock("credentials")
    credentials.expects(:authorization_header).once.returns("Bearer cached-token")
    Gcp::Credentials.expects(:new).once.with(scope: Storage::ObjectStore::SCOPE).returns(credentials)

    stub_request(:post, "https://storage.googleapis.com/upload/storage/v1/b/bucket-a/o?uploadType=media&name=prefix%2Fone.json")
      .with(headers: { "Authorization" => "Bearer cached-token", "Content-Type" => "application/json" })
      .to_return(status: 200, body: "{}")
    stub_request(:post, "https://storage.googleapis.com/upload/storage/v1/b/bucket-a/o?uploadType=media&name=prefix%2Ftwo.json")
      .with(headers: { "Authorization" => "Bearer cached-token", "Content-Type" => "application/json" })
      .to_return(status: 200, body: "{}")
    stub_request(:post, "https://storage.googleapis.com/upload/storage/v1/b/bucket-a/o?uploadType=media&name=prefix%2Fthree.json")
      .with(headers: { "Authorization" => "Bearer cached-token", "Content-Type" => "application/json" })
      .to_return(status: 200, body: "{}")

    store = Storage::ObjectStore::GcsStore.new(
      bucket: "bucket-a",
      prefix: "prefix",
      endpoint: "https://storage.googleapis.com"
    )

    uris = store.write_json_batch!(
      entries: [
        { object_path: "one.json", payload: { seq: 1 } },
        { object_path: "two.json", payload: { seq: 2 } },
        { object_path: "three.json", payload: { seq: 3 } }
      ]
    )

    assert_equal [
      "gs://bucket-a/prefix/one.json",
      "gs://bucket-a/prefix/two.json",
      "gs://bucket-a/prefix/three.json"
    ], uris
  end
end
