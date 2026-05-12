require "test_helper"

class HomeControllerTest < ActionDispatch::IntegrationTest
  test "shows the starter page" do
    get root_url

    assert_response :success
    assert_select "h1", "{{APP_NAME}}"
  end
end
