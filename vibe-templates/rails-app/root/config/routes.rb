Rails.application.routes.draw do
  root "home#index"
  get "up", to: ->(_env) { [200, { "Content-Type" => "text/plain; charset=utf-8" }, ["OK"]] }
end
