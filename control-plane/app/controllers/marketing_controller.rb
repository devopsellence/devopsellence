class MarketingController < ApplicationController
  layout "marketing"
  before_action :assign_public_contact_details

  LEGAL_EFFECTIVE_DATE = Date.new(2026, 3, 23)
  CONTACT_EMAIL = "contact@devopsellence.com"

  def index
    assign_public_install_command
  end

  def docs
    assign_public_install_command
  end

  def privacy
    assign_legal_page
  end

  def terms
    assign_legal_page
  end

  private
    def assign_public_contact_details
      @contact_email = CONTACT_EMAIL
    end

    def assign_public_install_command
      @cli_install_base_url = request.base_url
      @cli_install_command = "curl -fsSL #{@cli_install_base_url}/lfg.sh | bash"
      @agent_uninstall_command = "curl -fsSL #{@cli_install_base_url}/uninstall.sh | bash -s -- --purge-runtime"
    end

    def assign_legal_page
      @legal_effective_date = LEGAL_EFFECTIVE_DATE.strftime("%B %-d, %Y")
    end
end
