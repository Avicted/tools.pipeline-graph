import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.script
import jetbrains.buildServer.configs.kotlin.triggers.schedule

version = "2024.03"

project {
    description = "Weekly coordinated production release"
    val train = sequential {
        buildType(Freeze)
        parallel {
            buildType(AbsoluteId("OrdersService_Deploy_staging"))
            buildType(AbsoluteId("PaymentsService_Deploy_staging"))
        }
        buildType(StagingSmoke)
        parallel {
            buildType(AbsoluteId("OrdersService_Deploy_prod"))
            buildType(AbsoluteId("PaymentsService_Deploy_prod"))
            buildType(AbsoluteId("WebFrontend_PublishCdn"))
            buildType(AbsoluteId("Legacy_Billing_DeployProd"))
        }
        buildType(Release)
    }
    train.buildTypes().forEach { buildType(it) }
}

object Freeze : BuildType({
    name = "Code freeze & changelog"
    steps {
        script { scriptContent = "./scripts/changelog.sh" }
    }
    triggers {
        schedule {
            schedulingPolicy = weekly {
                dayOfWeek = ScheduleTrigger.DAY.Tuesday
                hour = 9
            }
            branchFilter = "+:<default>"
        }
    }
})

object StagingSmoke : BuildType({
    name = "Staging smoke suite"
    steps {
        script { scriptContent = "make smoke ENV=staging" }
    }
})

object Release : BuildType({
    name = "Release train"
    type = BuildTypeSettings.Type.COMPOSITE
})
