# Azure-serverless-go

This repo contains the code and instructions you'll need to ship logs from your Azure services to Logz.io.
At the end of this process, your Azure function will forward logs or metrics from an Azure Event Hub to your Logz.io account.

## Setting log shipping from Azure

### 1. Deploy the Logz.io template👇


[![Deploy to Azure](https://aka.ms/deploytoazurebutton)](https://portal.azure.com/#create/Microsoft.Template/uri/https%3A%2F%2Fraw.githubusercontent.com%2Flogzio%2Flogzio-azure-serverless%2Fmaster%2Fdeployments%2Fazuredeploylogs.json)

This deployment will create the following services:
* Serverless Function App
* Event Hubs Namespace
* Function's logs Storage Account
* Back up Storage Account for failed shipping
* App Service Plan
* Application Insights


### 2. Configure the template

Make sure to use these settings:

| Parameter                                                     | Description                                                                                                                                                     |
|---------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Resource group*                                               | Create a new resource group or select your existing one, and then click **OK**.                                                                                 |
| Region*                                                       | Select the same region as the Azure services that will stream data to this event hub.                                                                           |
| Debug*                                                        | Add debug logs to your function app.                                                                                                                            |
| Shipping token*                                               | Add the [logs shipping token](https://app.logz.io/#/dashboard/settings/general) for the relevant Logz.io account. This is the account you want to ship to.      |
| Logs listener url* (Default: `https://listener.logz.io:8071`) | Use the listener URL specific to the region of your Logz.io account. You can look it up [here](https://docs.logz.io/user-guide/accounts/account-region.html).   |

For all other parameters; to use your existing services, change the parameter to the relevant service's name. Otherwise, the template will build the necessary services automatically.

*Required fields.

At the bottom of the page, select **Review + Create**, and then click **Create** to deploy.

Deployment can take a few minutes.

### 3. Stream Azure service data to your new event hubs

Now that you've set it up, configure Azure to stream service logs to your new event hubs so that your new function apps can forward that data to Logz.io.
To send your data to this event hub choose your service type and create diagnostic settings for it.  
Under 'Event hub policy name' choose `LogzioSharedAccessKey` for logs.
This settings may take time to be applied and some services may need to be restarted.  
For more information see [Stream Azure monitoring data to an event hub for consumption by an external tool](https://docs.microsoft.com/en-us/azure/monitoring-and-diagnostics/monitor-stream-monitoring-data-event-hubs) from Microsoft.

![Diagnostic-settings](img/diagnostic-settings.png)

### 4. Check Logz.io for your data

Give your data some time to get from your system to ours, and then open Logz.io.
If everything went according to plan, you should see logs with the type `eventhub`.

### Backing up your logs!

This deployment will also back up your data in case of connection or shipping errors. In that case the logs that weren't shipped to Logz.io will be uploaded to the blob storage `logziologsbackupstorage` under the container `logziologsbackupcontainer`.

### Working with your parameters after deployment

If you wish to change parameters values after the deployment, go to your function app page, then on the left menu press the `Configuration` tab.
You'll have the option to edit the following values:
* Shipper's configurations such as LogzioListener, LogzioToken.
* FUNCTIONS_WORKER_PROCESS_COUNT - maximum of 10, for more information press [here](https://docs.microsoft.com/en-us/azure/azure-functions/functions-app-settings#functions_worker_process_count).


## Changelog

